package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// withStdin runs fn with os.Stdin replaced by a pipe pre-filled with content.
// It is implementation-agnostic: it works whether the code under test reads
// os.Stdin directly or via cobra's InOrStdin.
func withStdin(t *testing.T, content string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()
	fn()
}

// ndjsonRows parses every non-empty stdout line as a JSON object.
func ndjsonRows(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("non-JSON stdout line %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// rowsOfType filters parsed NDJSON rows by their "type" field.
func rowsOfType(rows []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, r := range rows {
		if r["type"] == typ {
			out = append(out, r)
		}
	}
	return out
}

// uidsOfType returns the numeric "uid" field of every row with the given type.
func uidsOfType(t *testing.T, rows []map[string]any, typ string) []uint32 {
	t.Helper()
	var out []uint32
	for _, r := range rowsOfType(rows, typ) {
		f, ok := r["uid"].(float64)
		if !ok {
			t.Fatalf("row %v has no numeric uid", r)
		}
		out = append(out, uint32(f))
	}
	return out
}

// summaryRow returns the single summary row, failing if absent or duplicated.
func summaryRow(t *testing.T, rows []map[string]any) map[string]any {
	t.Helper()
	sums := rowsOfType(rows, "summary")
	if len(sums) != 1 {
		t.Fatalf("want exactly 1 summary row, got %d", len(sums))
	}
	return sums[0]
}

func equalUIDs(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
