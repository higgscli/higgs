package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// resolveUIDList parses an explicit --uid value. The literal "-" reads the
// UID set from stdin; anything else is a comma-separated list.
func resolveUIDList(s string) ([]uint32, error) {
	if strings.TrimSpace(s) == "-" {
		uids, err := parseUIDsFromReader(os.Stdin)
		if err != nil {
			return nil, err
		}
		if len(uids) == 0 {
			return nil, fmt.Errorf("no UIDs found on stdin")
		}
		return uids, nil
	}
	return parseUIDList(s)
}

// parseUIDsFromReader reads a UID set from r, one line at a time. Lines
// starting with "{" are NDJSON objects: a numeric top-level "uid" field is
// taken, and object lines without one (e.g. {"type":"summary"} rows) are
// skipped, so higgs's own NDJSON output pipes in directly. All other lines
// are plain UID tokens separated by commas and/or whitespace. The result is
// deduplicated, preserving first-seen order.
func parseUIDsFromReader(r io.Reader) ([]uint32, error) {
	var out []uint32
	seen := make(map[uint32]bool)
	add := func(uid uint32) {
		if !seen[uid] {
			seen[uid] = true
			out = append(out, uid)
		}
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				return nil, fmt.Errorf("invalid JSON line on stdin: %w", err)
			}
			raw, ok := obj["uid"]
			if !ok {
				continue
			}
			f, ok := raw.(float64)
			if !ok || f != math.Trunc(f) || f < 0 || f > math.MaxUint32 {
				return nil, fmt.Errorf("invalid uid %v in JSON line on stdin", raw)
			}
			add(uint32(f))
			continue
		}
		for _, tok := range strings.FieldsFunc(line, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		}) {
			n, err := strconv.ParseUint(tok, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid UID %q on stdin: %w", tok, err)
			}
			add(uint32(n))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return out, nil
}
