package cerr

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestToEnvelopeOmitsEmptyHint(t *testing.T) {
	e := Validation("bad")
	env := e.ToEnvelope()
	inner, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error map: %#v", env)
	}
	if _, has := inner["hint"]; has {
		t.Errorf("hint should be omitted when empty")
	}
	if inner["kind"] != "validation" {
		t.Errorf("kind=%v", inner["kind"])
	}
	if inner["reason"] != "validationError" {
		t.Errorf("reason=%v", inner["reason"])
	}
	if inner["message"] != "bad" {
		t.Errorf("message=%v", inner["message"])
	}
	if inner["code"] != 400 {
		t.Errorf("code=%v", inner["code"])
	}
}

func TestToEnvelopeIncludesCause(t *testing.T) {
	cause := errors.New("ollama: parse JSON output: invalid character 'i'")
	inner := Classify(cause, "extract").ToEnvelope()["error"].(map[string]any)
	if inner["cause"] != cause.Error() {
		t.Errorf("cause=%v, want underlying error text", inner["cause"])
	}
}

func TestToEnvelopeOmitsRedundantCause(t *testing.T) {
	cause := errors.New("boom")
	// Message already embeds the cause text (classify-style wrapping).
	inner := Classify(cause, "%s", cause.Error()).ToEnvelope()["error"].(map[string]any)
	if _, has := inner["cause"]; has {
		t.Errorf("cause should be omitted when message already contains it")
	}
	inner = Validation("no cause here").ToEnvelope()["error"].(map[string]any)
	if _, has := inner["cause"]; has {
		t.Errorf("cause should be omitted when there is no cause")
	}
}

func TestToEnvelopeIncludesHint(t *testing.T) {
	e := API(403, "accessNotConfigured", "enable", "https://x")
	inner := e.ToEnvelope()["error"].(map[string]any)
	if inner["hint"] != "https://x" {
		t.Errorf("hint=%v", inner["hint"])
	}
}

func TestToJSONRoundTrip(t *testing.T) {
	e := API(404, "notFound", "missing doc", "")
	b, err := e.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON err: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	inner := decoded["error"].(map[string]any)
	if inner["kind"] != "api" {
		t.Errorf("kind=%v", inner["kind"])
	}
	if inner["code"].(float64) != 404 {
		t.Errorf("code=%v", inner["code"])
	}
	if inner["reason"] != "notFound" {
		t.Errorf("reason=%v", inner["reason"])
	}
	if inner["message"] != "missing doc" {
		t.Errorf("message=%v", inner["message"])
	}
	if _, has := inner["hint"]; has {
		t.Errorf("hint should not appear when empty")
	}
	// Indented output contains newline.
	if len(b) == 0 || b[0] != '{' {
		t.Errorf("expected JSON starting with {")
	}
}
