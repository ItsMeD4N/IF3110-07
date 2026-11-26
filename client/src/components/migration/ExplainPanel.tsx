import { useState } from 'react';
import { AlertTriangle, ChevronDown, ChevronUp, Gauge, Play, Sparkles } from 'lucide-react';
import { ApiError } from '@/lib/api';
import { formatNumber } from '@/lib/format';
import { explainSQL } from '@/services/migrations';
import type { ExplainResponse, SingleExplainResult } from '@/types/api';
import { Alert } from '@/components/ui/Alert';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';

interface ExplainPanelProps {
  dbId: number;
  sql: string;
  canAnalyze: boolean;
  label?: string;
}

export function ExplainPanel({ dbId, sql, canAnalyze, label = 'SQL' }: ExplainPanelProps) {
  const [loadingMode, setLoadingMode] = useState<'explain' | 'analyze' | null>(null);
  const [result, setResult] = useState<ExplainResponse | null>(null);
  const [error, setError] = useState('');
  const [expanded, setExpanded] = useState(true);

  const run = async (analyze: boolean) => {
    const trimmed = sql.trim();
    if (!trimmed) {
      setError(`Isi ${label} terlebih dahulu sebelum menjalankan EXPLAIN.`);
      return;
    }
    setError('');
    setLoadingMode(analyze ? 'analyze' : 'explain');
    try {
      const res = await explainSQL(dbId, trimmed, analyze);
      setResult(res);
      setExpanded(true);
    } catch (err) {
      setResult(null);
      if (err instanceof ApiError) {
        setError(err.detail ? `${err.message}: ${err.detail}` : err.message);
      } else {
        setError('Gagal menjalankan EXPLAIN.');
      }
    } finally {
      setLoadingMode(null);
    }
  };

  return (
    <div className="rounded-lg border border-border bg-surface-subtle/40 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-xs font-semibold text-ink">
          <Gauge className="h-3.5 w-3.5 text-accent" aria-hidden="true" />
          <span>Query Execution Plan ({label})</span>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            variant="secondary"
            size="sm"
            loading={loadingMode === 'explain'}
            disabled={loadingMode !== null || !sql.trim()}
            onClick={() => void run(false)}
          >
            <Sparkles className="h-3.5 w-3.5" aria-hidden="true" />
            <span>EXPLAIN</span>
          </Button>
          <Button
            variant="secondary"
            size="sm"
            loading={loadingMode === 'analyze'}
            disabled={!canAnalyze || loadingMode !== null || !sql.trim()}
            title={
              canAnalyze
                ? 'Menjalankan EXPLAIN ANALYZE di transaksi yang otomatis di-rollback (timeout 10 detik)'
                : 'EXPLAIN ANALYZE hanya tersedia untuk akses View + Edit'
            }
            onClick={() => void run(true)}
          >
            <Play className="h-3.5 w-3.5" aria-hidden="true" />
            <span>EXPLAIN ANALYZE</span>
          </Button>
          {result && (
            <Button
              variant="ghost"
              size="sm"
              aria-expanded={expanded}
              onClick={() => setExpanded((prev) => !prev)}
            >
              {expanded ? (
                <ChevronUp className="h-3.5 w-3.5" aria-hidden="true" />
              ) : (
                <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
              )}
              <span>{expanded ? 'Sembunyikan' : 'Tampilkan'}</span>
            </Button>
          )}
        </div>
      </div>

      {!canAnalyze && (
        <p className="mt-1.5 text-[11px] text-muted">
          Mode View Only mendukung <code className="font-mono">EXPLAIN</code> (transaksi read-only).{' '}
          <code className="font-mono">EXPLAIN ANALYZE</code> memerlukan akses View + Edit.
        </p>
      )}

      {error && (
        <Alert variant="error" className="mt-2.5">
          {error}
        </Alert>
      )}

      {result && expanded && (
        <div className="mt-3 space-y-3">
          {result.results.map((item, idx) => (
            <SinglePlanCard key={idx} item={item} index={idx + 1} />
          ))}
        </div>
      )}
    </div>
  );
}

function SinglePlanCard({ item, index }: { item: SingleExplainResult; index: number }) {
  const s = item.summary;
  return (
    <div className="rounded-md border border-border bg-surface p-3 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border pb-2">
        <span className="font-semibold text-ink tabular-nums">Statement #{index}</span>
        <div className="flex flex-wrap items-center gap-1.5">
          {item.is_explainable ? (
            <>
              <Badge tone={item.analyze_mode ? 'accent' : 'neutral'}>
                {item.analyze_mode ? 'EXPLAIN ANALYZE (rolled back)' : 'EXPLAIN (estimated)'}
              </Badge>
              {s.has_seq_scan && <Badge tone="warn">Seq Scan</Badge>}
            </>
          ) : (
            <Badge tone="neutral">Non-explainable (DDL)</Badge>
          )}
        </div>
      </div>

      <pre className="mt-2 overflow-x-auto rounded bg-surface-subtle px-2.5 py-1.5 font-mono text-[11px] text-ink">
        {item.statement}
      </pre>

      {!item.is_explainable ? (
        <p className="mt-2 text-muted">{item.note || 'Statement ini tidak memiliki execution plan.'}</p>
      ) : (
        <>
          <dl className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            <div className="rounded border border-border bg-surface-subtle/50 p-2">
              <dt className="text-[11px] text-muted">Scan Types</dt>
              <dd className="mt-0.5 font-mono font-medium text-ink">
                {s.scan_types && s.scan_types.length > 0 ? s.scan_types.join(', ') : '—'}
              </dd>
            </div>
            <div className="rounded border border-border bg-surface-subtle/50 p-2">
              <dt className="text-[11px] text-muted">Indexes Used</dt>
              <dd className="mt-0.5 font-mono font-medium text-ink">
                {s.indexes_used && s.indexes_used.length > 0 ? s.indexes_used.join(', ') : 'Tidak ada'}
              </dd>
            </div>
            <div className="rounded border border-border bg-surface-subtle/50 p-2">
              <dt className="text-[11px] text-muted">Estimated Cost</dt>
              <dd className="mt-0.5 font-mono font-medium text-ink tabular-nums">
                {s.startup_cost.toFixed(2)} .. {s.total_cost.toFixed(2)}
              </dd>
            </div>
            <div className="rounded border border-border bg-surface-subtle/50 p-2">
              <dt className="text-[11px] text-muted">
                {item.analyze_mode ? 'Actual Time / Rows' : 'Planned Rows'}
              </dt>
              <dd className="mt-0.5 font-mono font-medium text-ink tabular-nums">
                {item.analyze_mode && s.actual_time_ms !== undefined
                  ? `${s.actual_time_ms.toFixed(3)} ms · ${formatNumber(s.rows_processed ?? s.rows_planned)} rows`
                  : `${formatNumber(s.rows_planned)} rows`}
              </dd>
            </div>
          </dl>

          {s.join_strategies && s.join_strategies.length > 0 && (
            <p className="mt-2 text-[11px] text-muted">
              Join strategies:{' '}
              <span className="font-mono font-medium text-ink">{s.join_strategies.join(', ')}</span>
            </p>
          )}

          {s.warnings && s.warnings.length > 0 && (
            <div className="mt-2.5 space-y-1 rounded-md border border-warn/30 bg-warn-soft p-2.5 text-warn">
              {s.warnings.map((w, i) => (
                <p key={i} className="flex items-start gap-1.5">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                  <span>{w}</span>
                </p>
              ))}
            </div>
          )}

          {item.raw_text && (
            <pre className="mt-2.5 max-h-60 overflow-auto rounded bg-surface-subtle p-2.5 font-mono text-[11px] leading-relaxed text-ink">
              {item.raw_text}
            </pre>
          )}
        </>
      )}
    </div>
  );
}
