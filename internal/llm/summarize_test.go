package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higgscli/higgs/internal/cerr"
)

func newChatServer(t *testing.T, content string) (*httptest.Server, *atomic.Value) {
	t.Helper()
	captured := &atomic.Value{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.Store(string(body))
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message": map[string]interface{}{"content": content},
			"done":    true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func TestSummarize_Success(t *testing.T) {
	payload := `{"tldr":"Hello","bullets":["one","two"],"is_action_required":true,"due_date":"2026-05-01T00:00:00Z"}`
	srv, cap := newChatServer(t, payload)
	m := Message{UID: 1, From: "a@b.com", Subject: "Hi", Date: "2026-04-10T00:00:00Z", Body: strings.Repeat("x", 20000)}
	got, err := Summarize(context.Background(), tc(t, srv.URL), "m", m, SummarizeOpts{MaxBulletCount: 3, UserContext: "be terse", MaxInput: 100})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.TLDR != "Hello" || len(got.Bullets) != 2 || !got.IsActionRequired {
		t.Errorf("unexpected: %+v", got)
	}
	raw, _ := cap.Load().(string)
	if !strings.Contains(raw, "be terse") {
		t.Errorf("user context missing: %s", raw)
	}
	if !strings.Contains(raw, "Produce at most 3 bullets") {
		t.Errorf("bullet cap missing: %s", raw)
	}
}

func TestSummarize_DefaultOpts(t *testing.T) {
	payload := `{"tldr":"x","bullets":[],"is_action_required":false}`
	srv, _ := newChatServer(t, payload)
	m := Message{UID: 1, Body: "body"}
	got, err := Summarize(context.Background(), tc(t, srv.URL), "m", m, SummarizeOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.Bullets == nil {
		t.Error("bullets should be non-nil")
	}
}

func TestSummarize_4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	_, err := Summarize(context.Background(), tc(t, srv.URL), "m", Message{}, SummarizeOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
	if cerr.From(err).Kind != cerr.KindClassify {
		t.Errorf("kind = %v, want classify", cerr.From(err).Kind)
	}
}

func TestSummarize_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := Summarize(context.Background(), tc(t, srv.URL), "m", Message{}, SummarizeOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSummarize_MalformedOuter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()
	_, err := Summarize(context.Background(), tc(t, srv.URL), "m", Message{}, SummarizeOpts{})
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestSummarize_NonJSONContent(t *testing.T) {
	srv, _ := newChatServer(t, "not-json-inside")
	_, err := Summarize(context.Background(), tc(t, srv.URL), "m", Message{}, SummarizeOpts{})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSummarize_EmptyContent(t *testing.T) {
	srv, _ := newChatServer(t, "")
	_, err := Summarize(context.Background(), tc(t, srv.URL), "m", Message{}, SummarizeOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSummarize_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := Summarize(ctx, tc(t, srv.URL), "m", Message{}, SummarizeOpts{})
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestSummarizeThread_OrdersByDate(t *testing.T) {
	payload := `{"tldr":"t","bullets":["b"],"is_action_required":false}`
	srv, cap := newChatServer(t, payload)
	msgs := []Message{
		{UID: 2, Subject: "second", Date: "2026-04-10T00:00:00Z", Body: "later"},
		{UID: 1, Subject: "first", Date: "2026-04-01T00:00:00Z", Body: "earlier"},
	}
	got, err := SummarizeThread(context.Background(), tc(t, srv.URL), "m", msgs, SummarizeOpts{UserContext: "ctx"})
	if err != nil {
		t.Fatalf("SummarizeThread: %v", err)
	}
	if got.TLDR != "t" {
		t.Errorf("tldr=%q", got.TLDR)
	}
	raw, _ := cap.Load().(string)
	// first must come before second in the user prompt
	firstIdx := strings.Index(raw, "first")
	secondIdx := strings.Index(raw, "second")
	if firstIdx < 0 || secondIdx < 0 || firstIdx >= secondIdx {
		t.Errorf("thread ordering: first=%d second=%d raw=%s", firstIdx, secondIdx, raw)
	}
	if !strings.Contains(raw, "ctx") {
		t.Error("user context missing from thread prompt")
	}
}

func TestSummarizeThread_Empty(t *testing.T) {
	got, err := SummarizeThread(context.Background(), tc(t, "http://localhost"), "m", nil, SummarizeOpts{})
	if err != nil {
		t.Fatalf("empty thread: %v", err)
	}
	if got.Bullets == nil {
		t.Error("bullets should be non-nil")
	}
}

func TestSummarizeThread_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	_, err := SummarizeThread(context.Background(), tc(t, srv.URL), "m", []Message{{UID: 1}}, SummarizeOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseMaybeRFC3339(t *testing.T) {
	if !parseMaybeRFC3339("").IsZero() {
		t.Error("empty should be zero")
	}
	if parseMaybeRFC3339("2026-01-02T00:00:00Z").IsZero() {
		t.Error("RFC3339 should parse")
	}
	if parseMaybeRFC3339("Mon, 02 Jan 2026 15:04:05 +0000").IsZero() {
		t.Error("RFC1123Z should parse")
	}
	if !parseMaybeRFC3339("garbage").IsZero() {
		t.Error("garbage should be zero")
	}
}

func TestItoa(t *testing.T) {
	if itoa(0) != "0" {
		t.Error("0")
	}
	if itoa(1) != "1" {
		t.Error("1")
	}
	if itoa(42) != "42" {
		t.Error("42")
	}
	if itoa(-7) != "-7" {
		t.Error("-7")
	}
}

func TestBuildUserPrompt(t *testing.T) {
	m := Message{From: "a@b.com", Subject: "s", Date: "d", Body: "hello world"}
	got := buildUserPrompt(m, 5, "extra")
	if !strings.Contains(got, "From: a@b.com") || !strings.Contains(got, "Subject: s") || !strings.Contains(got, "Date: d") {
		t.Errorf("missing headers: %s", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("missing body: %s", got)
	}
	if strings.Contains(got, "world") {
		t.Errorf("body not trimmed: %s", got)
	}
	if !strings.Contains(got, "extra") {
		t.Errorf("missing extra: %s", got)
	}
	got = buildUserPrompt(Message{Body: "b"}, 0, "")
	if !strings.Contains(got, "b") {
		t.Errorf("no-trim path broken: %s", got)
	}
}
