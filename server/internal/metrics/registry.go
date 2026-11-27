package metrics

import (
	"bufio"
	"context"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	KindCounter   = "counter"
	KindGauge     = "gauge"
	KindHistogram = "histogram"
)

var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

type series struct {
	labelValues []string
	value       float64
	buckets     []uint64
	sum         float64
	count       uint64
}

type Family struct {
	name    string
	help    string
	kind    string
	labels  []string
	buckets []float64

	mu     sync.Mutex
	series map[string]*series
}

func (f *Family) key(values []string) []string {
	out := make([]string, len(f.labels))
	for i := range f.labels {
		if i < len(values) {
			out[i] = values[i]
		}
	}
	return out
}

func (f *Family) get(values []string) *series {
	vals := f.key(values)
	k := strings.Join(vals, "\xff")
	s, ok := f.series[k]
	if !ok {
		s = &series{labelValues: vals}
		if f.kind == KindHistogram {
			s.buckets = make([]uint64, len(f.buckets))
		}
		f.series[k] = s
	}
	return s
}

func (f *Family) Add(v float64, values ...string) {
	if f == nil || math.IsNaN(v) {
		return
	}
	if f.kind == KindCounter && v < 0 {
		return
	}
	f.mu.Lock()
	f.get(values).value += v
	f.mu.Unlock()
}

func (f *Family) Inc(values ...string) {
	f.Add(1, values...)
}

func (f *Family) Dec(values ...string) {
	if f == nil || f.kind != KindGauge {
		return
	}
	f.mu.Lock()
	f.get(values).value--
	f.mu.Unlock()
}

func (f *Family) Set(v float64, values ...string) {
	if f == nil || f.kind != KindGauge {
		return
	}
	f.mu.Lock()
	f.get(values).value = v
	f.mu.Unlock()
}

func (f *Family) Observe(v float64, values ...string) {
	if f == nil || f.kind != KindHistogram || math.IsNaN(v) {
		return
	}
	if v < 0 {
		v = 0
	}
	f.mu.Lock()
	s := f.get(values)
	for i, b := range f.buckets {
		if v <= b {
			s.buckets[i]++
		}
	}
	s.sum += v
	s.count++
	f.mu.Unlock()
}

func (f *Family) ObserveDuration(d time.Duration, values ...string) {
	f.Observe(d.Seconds(), values...)
}

type Sample struct {
	Labels []Label
	Value  float64
}

type Label struct {
	Name  string
	Value string
}

type Snapshot struct {
	Name    string
	Help    string
	Kind    string
	Samples []Sample
}

type CollectorFunc func(ctx context.Context) []Snapshot

type Registry struct {
	mu         sync.Mutex
	families   map[string]*Family
	collectors map[string]CollectorFunc
}

func NewRegistry() *Registry {
	return &Registry{families: map[string]*Family{}, collectors: map[string]CollectorFunc{}}
}

func (r *Registry) register(name, help, kind string, labels []string, buckets []float64) *Family {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.families[name]; ok {
		return f
	}
	if kind == KindHistogram && len(buckets) == 0 {
		buckets = DefaultBuckets
	}
	sorted := append([]float64(nil), buckets...)
	sort.Float64s(sorted)
	f := &Family{
		name:    name,
		help:    help,
		kind:    kind,
		labels:  append([]string(nil), labels...),
		buckets: sorted,
		series:  map[string]*series{},
	}
	r.families[name] = f
	return f
}

func (r *Registry) Counter(name, help string, labels ...string) *Family {
	return r.register(name, help, KindCounter, labels, nil)
}

func (r *Registry) Gauge(name, help string, labels ...string) *Family {
	return r.register(name, help, KindGauge, labels, nil)
}

func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *Family {
	return r.register(name, help, KindHistogram, labels, buckets)
}

func (r *Registry) RegisterCollector(id string, fn CollectorFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fn == nil {
		delete(r.collectors, id)
		return
	}
	r.collectors[id] = fn
}

func (r *Registry) WriteText(ctx context.Context, w io.Writer) error {
	r.mu.Lock()
	names := make([]string, 0, len(r.families))
	for n := range r.families {
		names = append(names, n)
	}
	fams := make([]*Family, 0, len(names))
	sort.Strings(names)
	for _, n := range names {
		fams = append(fams, r.families[n])
	}
	ids := make([]string, 0, len(r.collectors))
	for id := range r.collectors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	collectors := make([]CollectorFunc, 0, len(ids))
	for _, id := range ids {
		collectors = append(collectors, r.collectors[id])
	}
	r.mu.Unlock()

	bw := bufio.NewWriter(w)
	for _, f := range fams {
		writeFamily(bw, f)
	}

	merged := map[string]*Snapshot{}
	order := []string{}
	for _, c := range collectors {
		for _, snap := range c(ctx) {
			if existing, ok := merged[snap.Name]; ok {
				existing.Samples = append(existing.Samples, snap.Samples...)
				continue
			}
			s := snap
			merged[snap.Name] = &s
			order = append(order, snap.Name)
		}
	}
	sort.Strings(order)
	for _, name := range order {
		writeSnapshot(bw, merged[name])
	}
	return bw.Flush()
}

func writeHeader(w *bufio.Writer, name, help, kind string) {
	w.WriteString("# HELP ")
	w.WriteString(name)
	w.WriteByte(' ')
	w.WriteString(escapeHelp(help))
	w.WriteString("\n# TYPE ")
	w.WriteString(name)
	w.WriteByte(' ')
	w.WriteString(kind)
	w.WriteByte('\n')
}

func writeFamily(w *bufio.Writer, f *Family) {
	f.mu.Lock()
	keys := make([]string, 0, len(f.series))
	for k := range f.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	snap := make([]series, 0, len(keys))
	for _, k := range keys {
		s := f.series[k]
		cp := *s
		cp.labelValues = append([]string(nil), s.labelValues...)
		cp.buckets = append([]uint64(nil), s.buckets...)
		snap = append(snap, cp)
	}
	f.mu.Unlock()

	writeHeader(w, f.name, f.help, f.kind)
	for _, s := range snap {
		labels := make([]Label, len(f.labels))
		for i, n := range f.labels {
			labels[i] = Label{Name: n, Value: s.labelValues[i]}
		}
		if f.kind != KindHistogram {
			writeSample(w, f.name, labels, s.value)
			continue
		}
		for i, b := range f.buckets {
			writeSample(w, f.name+"_bucket", append(append([]Label(nil), labels...), Label{"le", formatFloat(b)}), float64(s.buckets[i]))
		}
		writeSample(w, f.name+"_bucket", append(append([]Label(nil), labels...), Label{"le", "+Inf"}), float64(s.count))
		writeSample(w, f.name+"_sum", labels, s.sum)
		writeSample(w, f.name+"_count", labels, float64(s.count))
	}
}

func writeSnapshot(w *bufio.Writer, s *Snapshot) {
	writeHeader(w, s.Name, s.Help, s.Kind)
	for _, sm := range s.Samples {
		writeSample(w, s.Name, sm.Labels, sm.Value)
	}
}

func writeSample(w *bufio.Writer, name string, labels []Label, v float64) {
	w.WriteString(name)
	if len(labels) > 0 {
		w.WriteByte('{')
		for i, l := range labels {
			if i > 0 {
				w.WriteByte(',')
			}
			w.WriteString(l.Name)
			w.WriteString(`="`)
			w.WriteString(escapeLabel(l.Value))
			w.WriteByte('"')
		}
		w.WriteByte('}')
	}
	w.WriteByte(' ')
	w.WriteString(formatFloat(v))
	w.WriteByte('\n')
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func escapeLabel(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

func escapeHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = r.WriteText(ctx, w)
	})
}
