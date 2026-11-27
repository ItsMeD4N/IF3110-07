package telemetry

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogOutputWritesStdoutAndFile(t *testing.T) {
	var stdout bytes.Buffer
	path := filepath.Join(t.TempDir(), "nested", "app.log")
	w, closeFn, err := LogOutput(&stdout, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`{"event":"x"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	closeFn()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"event":"x"`) || !strings.Contains(stdout.String(), `"event":"x"`) {
		t.Fatalf("log harus tertulis ke stdout dan file: stdout=%q file=%q", stdout.String(), data)
	}
}

func TestLogOutputWithoutFile(t *testing.T) {
	var stdout bytes.Buffer
	w, closeFn, err := LogOutput(&stdout, "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if w != &stdout {
		t.Fatal("tanpa LOG_FILE output harus stdout saja")
	}
}
