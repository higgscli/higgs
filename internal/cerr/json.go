package cerr

import (
	"encoding/json"
	"strings"
)

// ToEnvelope returns a JSON-ready map describing the error.
func (e *Error) ToEnvelope() map[string]any {
	inner := map[string]any{
		"kind":    e.Kind.String(),
		"code":    e.Code,
		"reason":  e.Reason,
		"message": e.Message,
	}
	if e.Hint != "" {
		inner["hint"] = e.Hint
	}
	if cause := e.causeText(); cause != "" {
		inner["cause"] = cause
	}
	return map[string]any{"error": inner}
}

// causeText returns the underlying cause's text when it adds information the
// message doesn't already contain. Without this, wrappers like
// Classify(err, "extract") reduce every failure to the same opaque envelope.
func (e *Error) causeText() string {
	if e.Cause == nil {
		return ""
	}
	cause := e.Cause.Error()
	if cause == "" || strings.Contains(e.Message, cause) {
		return ""
	}
	return cause
}

// ToJSON returns the indented JSON envelope.
func (e *Error) ToJSON() ([]byte, error) {
	return json.MarshalIndent(e.ToEnvelope(), "", "  ")
}
