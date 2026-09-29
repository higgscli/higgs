package jev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuestionMarshalPreservesChoiceOrder(t *testing.T) {
	q := Question{Type: KindChoice, Instructions: "pick", Options: []Option{
		{Key: "zeta", Description: "last alphabetically"},
		{Key: "alpha", Description: "first \"quoted\""},
	}}
	got, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"choice","instructions":"pick","criteria":{"zeta":"last alphabetically","alpha":"first \"quoted\""}}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestQuestionMarshalNoulHasNoCriteria(t *testing.T) {
	got, _ := json.Marshal(Question{Type: KindNoul, Instructions: "bulk?"})
	if want := `{"type":"noul","instructions":"bulk?"}`; string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestDecide(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		io.WriteString(w, `{"answers":{
			"bulk":{"type":"noul","noul":0.91},
			"cat":{"type":"choice","choice":"b","probabilities":{"a":0.2,"b":0.8},"confidence":0.6}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL + "/")
	ans, err := c.Decide(context.Background(), "the email", map[string]Question{
		"bulk": {Type: KindNoul, Instructions: "bulk?"},
		"cat":  {Type: KindChoice, Instructions: "which?", Options: []Option{{Key: "a"}, {Key: "b"}}},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if gotPath != "/v1/systemone" {
		t.Errorf("path=%q", gotPath)
	}
	if !strings.Contains(gotBody, `"state":"the email"`) {
		t.Errorf("body missing state: %s", gotBody)
	}
	if ans["bulk"].Noul != 0.91 || ans["cat"].Choice != "b" || ans["cat"].Probabilities["b"] != 0.8 {
		t.Errorf("answers=%+v", ans)
	}
}

func TestDecideErrors(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		wantErr    string
	}{
		{"http error", `{"error":"boom"}`, http.StatusInternalServerError, "500"},
		{"bad json", `not json`, http.StatusOK, "parse jev response"},
		{"missing answer", `{"answers":{}}`, http.StatusOK, `missing answer for "q"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := New(srv.URL).Decide(context.Background(), "s", map[string]Question{"q": {Type: KindNoul}})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestDecideUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := New(url).Decide(context.Background(), "s", map[string]Question{"q": {Type: KindNoul}})
	if err == nil || !strings.Contains(err.Error(), "jev server") {
		t.Fatalf("err=%v", err)
	}
}

func TestBaseURLFromEnv(t *testing.T) {
	t.Setenv("PM_JEV_BASE_URL", "")
	if got := BaseURLFromEnv(); got != DefaultBaseURL {
		t.Errorf("default=%q", got)
	}
	t.Setenv("PM_JEV_BASE_URL", " http://gpu-box:8791/ ")
	if got := BaseURLFromEnv(); got != "http://gpu-box:8791" {
		t.Errorf("got %q", got)
	}
}
