package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
)

func TestDigest_Success(t *testing.T) {
	payload := `{"window":"7d","highlights":[{"uid":1,"title":"T","summary":"S","is_actionable":true}],"by_category":{"Finance":["bill"]},"counts":{"Finance":1}}`
	srv, cap := newChatServer(t, payload)
	msgs := []Message{
		{UID: 1, Subject: "bill", From: "a@b.com", Date: "2026-04-01T00:00:00Z", Body: "pay this"},
		{UID: 2, Subject: "promo", From: "c@d.com", Date: "2026-04-02T00:00:00Z", Body: "sale"},
	}
	got, err := BuildDigest(context.Background(), tc(t, srv.URL), "m", msgs, DigestOpts{
		Window:          "7d",
		CanonicalLabels: []string{"Finance", "Promotions"},
		UserContext:     "focus on deadlines",
	})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if got.Window != "7d" || len(got.Highlights) != 1 {
		t.Errorf("unexpected: %+v", got)
	}
	if got.Counts["Finance"] != 1 {
		t.Errorf("counts: %+v", got.Counts)
	}
	raw, _ := cap.Load().(string)
	if !strings.Contains(raw, "Finance, Promotions") {
		t.Errorf("canonical labels missing: %s", raw)
	}
	if !strings.Contains(raw, "focus on deadlines") {
		t.Errorf("user context missing: %s", raw)
	}
	if !strings.Contains(raw, "uid=1") || !strings.Contains(raw, "uid=2") {
		t.Errorf("uids missing: %s", raw)
	}
}

func TestDigest_Defaults(t *testing.T) {
	payload := `{"window":"","highlights":[],"by_category":{},"counts":{}}`
	srv, _ := newChatServer(t, payload)
	got, err := BuildDigest(context.Background(), tc(t, srv.URL), "m", []Message{{UID: 1, Body: "b"}}, DigestOpts{Window: "24h"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if got.Window != "24h" {
		t.Errorf("window default not applied: %q", got.Window)
	}
	if got.Highlights == nil || got.ByCategory == nil || got.Counts == nil {
		t.Error("zero maps/slices not initialized")
	}
}

func TestDigest_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := BuildDigest(context.Background(), tc(t, srv.URL), "m", []Message{{UID: 1}}, DigestOpts{Window: "1d"})
	if err == nil {
		t.Fatal("expected error")
	}
	if cerr.From(err).Kind != cerr.KindClassify {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestDigest_MalformedOuter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	_, err := BuildDigest(context.Background(), tc(t, srv.URL), "m", nil, DigestOpts{Window: "1d"})
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestDigest_NonJSONContent(t *testing.T) {
	srv, _ := newChatServer(t, "plain text")
	_, err := BuildDigest(context.Background(), tc(t, srv.URL), "m", nil, DigestOpts{Window: "1d"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDigest_EmptyContent(t *testing.T) {
	srv, _ := newChatServer(t, "")
	_, err := BuildDigest(context.Background(), tc(t, srv.URL), "m", nil, DigestOpts{Window: "1d"})
	if err == nil {
		t.Fatal("expected empty error")
	}
}

func TestDigest_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := BuildDigest(ctx, tc(t, srv.URL), "m", nil, DigestOpts{Window: "1d"})
	if err == nil {
		t.Fatal("expected cancel error")
	}
}
