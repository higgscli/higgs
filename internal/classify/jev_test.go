package classify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/jev"
	"github.com/higgscli/higgs/internal/labels"
)

func TestJevResult(t *testing.T) {
	cases := []struct {
		name       string
		bulk       float64
		probs      map[string]float64
		wantLabels []string
		wantML     bool
		wantConf   float64
	}{
		{"single label", 0.1, map[string]float64{"Finance": 0.8, "Orders": 0.15, "Jobs": 0.05}, []string{"Finance"}, false, 0.8},
		{"runner-up above threshold", 0.2, map[string]float64{"Orders": 0.55, "Finance": 0.35, "Jobs": 0.1}, []string{"Orders", "Finance"}, false, 0.55},
		{"never more than two", 0.2, map[string]float64{"Orders": 0.34, "Finance": 0.33, "Jobs": 0.33}, []string{"Orders", "Finance"}, false, 0.34},
		{"bulk flag", 0.9, map[string]float64{"Promotions": 0.7, "Newsletters": 0.3}, []string{"Promotions", "Newsletters"}, true, 0.7},
		{"tie broken by key", 0.2, map[string]float64{"Travel": 0.5, "Health": 0.5}, []string{"Health", "Travel"}, false, 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jevResult(map[string]jev.Answer{
				jevMailingListKey: {Noul: tc.bulk},
				jevCategoryKey:    {Probabilities: tc.probs},
			})
			if !reflect.DeepEqual(got.SuggestedLabels, tc.wantLabels) {
				t.Errorf("labels=%v want %v", got.SuggestedLabels, tc.wantLabels)
			}
			if got.IsMailingList != tc.wantML || got.Confidence != tc.wantConf {
				t.Errorf("is_mailing_list=%v confidence=%v", got.IsMailingList, got.Confidence)
			}
			if !strings.HasPrefix(got.Rationale, "jev: ") {
				t.Errorf("rationale=%q", got.Rationale)
			}
		})
	}
}

func TestJevQuestionsCoverTaxonomy(t *testing.T) {
	q := jevQuestions()
	cat := q[jevCategoryKey]
	if cat.Type != jev.KindChoice {
		t.Fatalf("category type=%q", cat.Type)
	}
	var keys []string
	for _, o := range cat.Options {
		keys = append(keys, o.Key)
		if o.Description == "" {
			t.Errorf("option %q has no description", o.Key)
		}
	}
	if !reflect.DeepEqual(keys, labels.Default.Canonical()) {
		t.Errorf("options=%v want taxonomy %v", keys, labels.Default.Canonical())
	}
	if q[jevMailingListKey].Type != jev.KindNoul {
		t.Errorf("mailing_list type=%q", q[jevMailingListKey].Type)
	}
}

func TestJevStateTruncatesBodyByRune(t *testing.T) {
	msg := &email.Message{From: "a@b", Subject: "hi", BodySnippet: strings.Repeat("é", JevBodyChars+50)}
	s := jevState(msg)
	body := s[strings.Index(s, "Body: ")+len("Body: "):]
	if n := len([]rune(body)); n != JevBodyChars {
		t.Fatalf("body runes=%d want %d", n, JevBodyChars)
	}
}

func TestJevClassifierEndToEnd(t *testing.T) {
	var req struct {
		State     string                     `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &req); err != nil {
			t.Errorf("bad request json: %v", err)
		}
		io.WriteString(w, `{"answers":{
			"mailing_list":{"type":"noul","noul":0.97},
			"category":{"type":"choice","choice":"Promotions","probabilities":{"Promotions":0.9,"Orders":0.1}}}}`)
	}))
	defer srv.Close()

	c := JevClassifier{Client: jev.New(srv.URL)}
	res, err := c.Classify(context.Background(), &email.Message{UID: 7, From: "deals@shop.example", Subject: "50% off", BodySnippet: "sale"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !strings.Contains(req.State, "Subject: 50% off") || len(req.Questions) != 2 {
		t.Errorf("request=%+v", req)
	}
	if !res.IsMailingList || res.Confidence != 0.9 || !reflect.DeepEqual(res.SuggestedLabels, []string{"Promotions"}) {
		t.Errorf("result=%+v", res)
	}
}

func TestJevClassifierBulkAddsNewsletters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"answers":{
			"mailing_list":{"type":"noul","noul":0.8},
			"category":{"type":"choice","probabilities":{"Jobs":0.95,"Orders":0.05}}}}`)
	}))
	defer srv.Close()
	res, err := JevClassifier{Client: jev.New(srv.URL)}.Classify(context.Background(), &email.Message{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.SuggestedLabels, []string{"Jobs", "Newsletters"}) {
		t.Errorf("labels=%v", res.SuggestedLabels)
	}
}

func TestJevClassifierServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model loading", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, err := JevClassifier{Client: jev.New(srv.URL)}.Classify(context.Background(), &email.Message{})
	if err == nil || !strings.HasPrefix(err.Error(), "jev: ") {
		t.Fatalf("err=%v", err)
	}
}
