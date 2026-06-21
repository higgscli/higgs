package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
)

func TestPreset_KnownNames(t *testing.T) {
	for _, name := range []string{"invoice", "shipping", "meeting"} {
		schema, err := Preset(name)
		if err != nil {
			t.Errorf("Preset(%q): %v", name, err)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("Preset(%q) has no type=object: %v", name, schema)
		}
		if _, ok := schema["properties"]; !ok {
			t.Errorf("Preset(%q) missing properties", name)
		}
	}
}

func TestPreset_Unknown(t *testing.T) {
	if _, err := Preset("nonsense"); err == nil {
		t.Fatal("expected error for unknown preset")
	}
}

func TestExtract_Success(t *testing.T) {
	payload := `{"amount": 42, "currency": "USD", "vendor": "Acme"}`
	srv, _ := newChatServer(t, payload)
	schema, err := Preset("invoice")
	if err != nil {
		t.Fatalf("preset: %v", err)
	}
	got, err := Extract(context.Background(), srv.URL, "m", Message{UID: 1, Body: "invoice text"}, schema)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got["vendor"] != "Acme" {
		t.Errorf("vendor: %v", got)
	}
}

func TestExtract_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	_, err := Extract(context.Background(), srv.URL, "m", Message{}, map[string]any{"type": "object"})
	if err == nil {
		t.Fatal("expected error")
	}
	if cerr.From(err).Kind != cerr.KindClassify {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestExtract_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := Extract(context.Background(), srv.URL, "m", Message{}, map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtract_MalformedOuter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	_, err := Extract(context.Background(), srv.URL, "m", Message{}, map[string]any{})
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestExtract_NonJSONContent(t *testing.T) {
	srv, _ := newChatServer(t, "not-json")
	_, err := Extract(context.Background(), srv.URL, "m", Message{}, map[string]any{})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestExtract_EmptyContent(t *testing.T) {
	srv, _ := newChatServer(t, "")
	_, err := Extract(context.Background(), srv.URL, "m", Message{}, map[string]any{})
	if err == nil {
		t.Fatal("expected empty error")
	}
}

func TestExtract_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Extract(ctx, srv.URL, "m", Message{}, map[string]any{})
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestExtract_NullResult(t *testing.T) {
	// Content is literal JSON null — decode succeeds, output should be empty map.
	srv, _ := newChatServer(t, "null")
	got, err := Extract(context.Background(), srv.URL, "m", Message{}, map[string]any{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got == nil {
		t.Error("expected empty map, got nil")
	}
}
