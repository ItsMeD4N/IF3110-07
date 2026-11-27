package telemetry

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func LogOutput(stdout io.Writer, path string) (io.Writer, func(), error) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if path == "" {
		return stdout, func() {}, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("telemetry: buat folder log %q: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: buka file log %q: %w", path, err)
	}
	return io.MultiWriter(stdout, f), func() { _ = f.Close() }, nil
}
