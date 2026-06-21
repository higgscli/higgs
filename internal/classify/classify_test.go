package classify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/labels"
)

func TestAllowedLabelsMatchesTaxonomy(t *testing.T) {
	got := AllowedLabels()
	want := labels.Default.Canonical()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllowedLabels()=%v, want %v", got, want)
	}
}

func TestNormalizeLabels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"canonical-passthrough", []string{"Orders"}, []string{"Orders"}},
		{"alias-lowercase", []string{"orders"}, []string{"Orders"}},
		{"alias-map", []string{"amazon"}, []string{"Orders"}},
		{"labels-prefix", []string{"Labels/Orders"}, []string{"Orders"}},
		{"whitespace", []string{"  Orders  "}, []string{"Orders"}},
		{"dedupe", []string{"Orders", "orders", "order"}, []string{"Orders"}},
		{"unknown-dropped", []string{"completely-unknown-thing"}, []string{}},
		{"mixed-known-unknown", []string{"Finance", "xxxx", "Security"}, []string{"Finance", "Security"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeLabels(tc.in)
			// Normalize: treat nil as empty slice.
			if got == nil {
				got = []string{}
			}
			want := tc.want
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("normalizeLabels(%v)=%v, want %v", tc.in, got, want)
			}
		})
	}
}

func TestClassificationSchemaShape(t *testing.T) {
	schema := ClassificationSchema
	if schema["type"] != "object" {
		t.Errorf(`schema["type"]=%v, want "object"`, schema["type"])
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf(`schema["properties"] missing or wrong type: %T`, schema["properties"])
	}
	for _, key := range []string{"suggested_labels", "confidence", "rationale", "is_mailing_list"} {
		if _, ok := props[key]; !ok {
			t.Errorf("property %q missing", key)
		}
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf(`schema["required"] missing or wrong type: %T`, schema["required"])
	}
	sort.Strings(required)
	want := []string{"confidence", "is_mailing_list", "rationale", "suggested_labels"}
	if !reflect.DeepEqual(required, want) {
		t.Errorf("required=%v, want %v", required, want)
	}

	// Drill into suggested_labels.items.enum and confirm it matches Canonical.
	sl, ok := props["suggested_labels"].(map[string]interface{})
	if !ok {
		t.Fatalf("suggested_labels not a map: %T", props["suggested_labels"])
	}
	items, ok := sl["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("items not a map: %T", sl["items"])
	}
	enum, ok := items["enum"].([]string)
	if !ok {
		t.Fatalf("enum not []string: %T", items["enum"])
	}
	gotEnum := append([]string(nil), enum...)
	sort.Strings(gotEnum)
	want2 := labels.Default.Canonical()
	sort.Strings(want2)
	if !reflect.DeepEqual(gotEnum, want2) {
		t.Errorf("enum=%v, want %v", gotEnum, want2)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"", 5, ""},
		{"short", 10, "short"},
		{"exactly-ten", 11, "exactly-ten"},
		{"this is long", 4, "this…"},
	}
	for _, tc := range cases {
		got := truncate(tc.in, tc.max)
		if got != tc.want {
			t.Errorf("truncate(%q,%d)=%q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

// fakeOllama is a tiny httptest-backed Ollama server. `respond` returns the
// body of the /api/chat response (the JSON wire format), with the inner
// "message.content" being an arbitrary string (typically a JSON blob for the
// classifier schema).
type fakeOllamaOpts struct {
	status      int
	content     string // goes into message.content
	rawResponse string // if non-empty, replaces the whole response body
	delay       time.Duration
}

func newFakeOllama(t *testing.T, opts fakeOllamaOpts) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts.delay > 0 {
			select {
			case <-time.After(opts.delay):
			case <-r.Context().Done():
				return
			}
		}
		if opts.status != 0 {
			w.WriteHeader(opts.status)
			_, _ = io.WriteString(w, "backend error")
			return
		}
		if opts.rawResponse != "" {
			_, _ = io.WriteString(w, opts.rawResponse)
			return
		}
		body := map[string]interface{}{
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": opts.content,
			},
			"done": true,
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestClassify_Success(t *testing.T) {
	content, _ := json.Marshal(Result{
		SuggestedLabels: []string{"Orders"},
		Confidence:      0.9,
		Rationale:       "amazon shipment",
		IsMailingList:   false,
	})
	srv := newFakeOllama(t, fakeOllamaOpts{content: string(content)})
	defer srv.Close()

	msg := &email.Message{UID: 1, From: "amazon", Subject: "Order", BodySnippet: "tracking"}
	got, err := Classify(context.Background(), srv.URL, "test-model", msg)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !reflect.DeepEqual(got.SuggestedLabels, []string{"Orders"}) {
		t.Errorf("labels=%v", got.SuggestedLabels)
	}
	if got.Confidence != 0.9 {
		t.Errorf("confidence=%v", got.Confidence)
	}
}

func TestClassify_AddsNewsletterFallbackWhenMailingList(t *testing.T) {
	content, _ := json.Marshal(Result{
		SuggestedLabels: []string{"Jobs"},
		IsMailingList:   true,
	})
	srv := newFakeOllama(t, fakeOllamaOpts{content: string(content)})
	defer srv.Close()

	got, err := Classify(context.Background(), srv.URL, "m", &email.Message{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	want := []string{"Jobs", "Newsletters"}
	if !reflect.DeepEqual(got.SuggestedLabels, want) {
		t.Errorf("labels=%v, want %v", got.SuggestedLabels, want)
	}
}

func TestClassify_MailingListWithPromotionsKept(t *testing.T) {
	content, _ := json.Marshal(Result{
		SuggestedLabels: []string{"Promotions"},
		IsMailingList:   true,
	})
	srv := newFakeOllama(t, fakeOllamaOpts{content: string(content)})
	defer srv.Close()

	got, err := Classify(context.Background(), srv.URL, "m", &email.Message{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	// Should not have added Newsletters since Promotions already present.
	for _, l := range got.SuggestedLabels {
		if l == "Newsletters" {
			t.Errorf("unexpected Newsletters label in %v", got.SuggestedLabels)
		}
	}
}

func TestClassify_MailingListWithNewslettersKept(t *testing.T) {
	content, _ := json.Marshal(Result{
		SuggestedLabels: []string{"Newsletters"},
		IsMailingList:   true,
	})
	srv := newFakeOllama(t, fakeOllamaOpts{content: string(content)})
	defer srv.Close()

	got, err := Classify(context.Background(), srv.URL, "m", &email.Message{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(got.SuggestedLabels) != 1 || got.SuggestedLabels[0] != "Newsletters" {
		t.Errorf("labels=%v", got.SuggestedLabels)
	}
}

func TestClassify_HTTPError(t *testing.T) {
	srv := newFakeOllama(t, fakeOllamaOpts{status: http.StatusInternalServerError})
	defer srv.Close()

	_, err := Classify(context.Background(), srv.URL, "m", &email.Message{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ollama") {
		t.Errorf("error should mention ollama: %v", err)
	}
}

func TestClassify_MalformedJSON(t *testing.T) {
	srv := newFakeOllama(t, fakeOllamaOpts{rawResponse: "not json at all"})
	defer srv.Close()

	_, err := Classify(context.Background(), srv.URL, "m", &email.Message{})
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestClassify_NonJSONContent(t *testing.T) {
	// message.content is not JSON, so parsing the schema out of it fails.
	srv := newFakeOllama(t, fakeOllamaOpts{content: "hello world"})
	defer srv.Close()

	_, err := Classify(context.Background(), srv.URL, "m", &email.Message{})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestClassify_ContextCancel(t *testing.T) {
	// Server delays long enough that context cancellation wins.
	srv := newFakeOllama(t, fakeOllamaOpts{content: `{}`, delay: 500 * time.Millisecond})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := Classify(ctx, srv.URL, "m", &email.Message{})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// Ensure the request body shape is what Ollama expects.
func TestClassify_SendsExpectedRequestBody(t *testing.T) {
	var captured atomic.Value
	content, _ := json.Marshal(Result{SuggestedLabels: []string{"Orders"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.Store(string(body))
		resp := map[string]interface{}{
			"message": map[string]interface{}{"content": string(content)},
			"done":    true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_, err := Classify(context.Background(), srv.URL, "my-model", &email.Message{From: "a", Subject: "b", BodySnippet: "c"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	raw, _ := captured.Load().(string)
	if raw == "" {
		t.Fatal("no captured body")
	}
	if !strings.Contains(raw, `"model":"my-model"`) {
		t.Errorf("body missing model: %s", raw)
	}
	if !strings.Contains(raw, `"format":`) {
		t.Errorf("body missing format: %s", raw)
	}
	if !strings.Contains(raw, `"stream":false`) {
		t.Errorf("body missing stream:false: %s", raw)
	}
}

// Sanity: truncate is invoked with long subjects without crashing.
func TestClassify_LogsTruncatedSubject(t *testing.T) {
	content, _ := json.Marshal(Result{SuggestedLabels: []string{"Orders"}})
	srv := newFakeOllama(t, fakeOllamaOpts{content: string(content)})
	defer srv.Close()

	msg := &email.Message{Subject: strings.Repeat("x", 200)}
	if _, err := Classify(context.Background(), srv.URL, "m", msg); err != nil {
		t.Fatalf("Classify: %v", err)
	}
}
