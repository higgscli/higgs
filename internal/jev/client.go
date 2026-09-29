// Package jev is a client for Jev-style decision servers (the Open-Jev
// `/v1/systemone` API). A decision model does not generate text: it scores
// caller-supplied options in one forward pass and returns calibrated
// probabilities for typed questions — yes/no ("noul"), one-of-N ("choice")
// and ordinal ("score").
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is where `python -m jev.server` listens by default.
const DefaultBaseURL = "http://127.0.0.1:8791"

// Question kinds used by higgs. The server also supports "score" (0–5
// ordinal), which higgs does not need yet.
const (
	KindNoul   = "noul"
	KindChoice = "choice"
)

// Question is one typed question about the request state. Options is used
// only by choice questions and is sent in order.
type Question struct {
	Type         string
	Instructions string
	Options      []Option
}

// Option is one candidate answer to a choice question.
type Option struct {
	Key         string
	Description string
}

// MarshalJSON renders the Open-Jev wire shape: choice criteria are a JSON
// object mapping option key -> description (insertion order preserved).
func (q Question) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"type":`)
	writeJSON(&b, q.Type)
	b.WriteString(`,"instructions":`)
	writeJSON(&b, q.Instructions)
	if q.Type == KindChoice {
		b.WriteString(`,"criteria":{`)
		for i, o := range q.Options {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(&b, o.Key)
			b.WriteByte(':')
			writeJSON(&b, o.Description)
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func writeJSON(b *bytes.Buffer, v any) {
	enc, _ := json.Marshal(v)
	b.Write(enc)
}

// Answer is the server's reply to one question.
type Answer struct {
	Type string `json:"type"`
	// Noul is P(yes) for a noul question.
	Noul float64 `json:"noul"`
	// Choice is the highest-probability option key for a choice question.
	Choice string `json:"choice"`
	// Probabilities maps option key -> probability (choice and score).
	Probabilities map[string]float64 `json:"probabilities"`
	// Confidence is the server's confidence in Choice.
	Confidence float64 `json:"confidence"`
}

type request struct {
	State     string              `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type response struct {
	Answers map[string]Answer `json:"answers"`
	Error   any               `json:"error,omitempty"`
}

// Client talks to one decision server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for baseURL (trailing slashes trimmed).
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 2 * time.Minute},
	}
}

// BaseURLFromEnv reads PM_JEV_BASE_URL, falling back to DefaultBaseURL.
func BaseURLFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("PM_JEV_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultBaseURL
}

// Decide asks every question in qs about state in a single request and
// returns the answers keyed like qs. Every requested key must be answered.
func (c *Client) Decide(ctx context.Context, state string, qs map[string]Question) (map[string]Answer, error) {
	body, err := json.Marshal(request{State: state, Questions: qs})
	if err != nil {
		return nil, fmt.Errorf("encode jev request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build jev request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jev server %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read jev response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jev server returned %s: %s", resp.Status, clip(string(raw), 300))
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse jev response: %w (raw: %q)", err, clip(string(raw), 300))
	}
	for key := range qs {
		if _, ok := out.Answers[key]; !ok {
			return nil, fmt.Errorf("jev response missing answer for %q", key)
		}
	}
	return out.Answers, nil
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
