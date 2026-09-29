package classify

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/jev"
	"github.com/higgscli/higgs/internal/labels"
	"github.com/higgscli/higgs/internal/llmclient"
)

// Classifier classifies a single message. Implementations must be safe for
// concurrent use by the classify worker pool.
type Classifier interface {
	Classify(ctx context.Context, msg *email.Message) (*Result, error)
}

// LLMClassifier classifies with a generative chat model via llmclient.
type LLMClassifier struct {
	Client llmclient.Client
	Model  string
}

// Classify implements Classifier.
func (c LLMClassifier) Classify(ctx context.Context, msg *email.Message) (*Result, error) {
	return Classify(ctx, c.Client, c.Model, msg)
}

const (
	// JevBodyChars caps the body sent to a decision server. Decision latency
	// grows with prompt length; 1,200 characters kept ranking quality within
	// noise of longer snippets in testing.
	JevBodyChars = 1200
	// jevSecondLabelMin is the probability the runner-up category needs to
	// be suggested as a second label (the LLM path allows one or two).
	jevSecondLabelMin = 0.3

	jevMailingListKey = "mailing_list"
	jevCategoryKey    = "category"
)

// jevMailingListQuestion mirrors the LLM prompt's is_mailing_list rule
// (unsubscribe / mailing-list language), which covers automated
// notifications too. Naming notification-settings links explicitly raised
// bulk recall from 8/18 to 14/18 at one personal false positive in 35 in
// testing, versus asking only about newsletters and marketing.
const jevMailingListQuestion = "Is this an automated or mass-sent email (notifications, alerts, newsletters, promotions, anything with unsubscribe or notification-settings links), not a personal message from a person?"

// JevClassifier classifies with a Jev decision server: one yes/no question
// for mailing-list detection and one choice over the label taxonomy, both
// answered in a single request with calibrated probabilities.
type JevClassifier struct {
	Client *jev.Client
}

// Classify implements Classifier.
func (c JevClassifier) Classify(ctx context.Context, msg *email.Message) (*Result, error) {
	answers, err := c.Client.Decide(ctx, jevState(msg), jevQuestions())
	if err != nil {
		return nil, fmt.Errorf("jev: %w", err)
	}
	return finalize(jevResult(answers), msg), nil
}

func jevState(msg *email.Message) string {
	body := msg.BodySnippet
	if r := []rune(body); len(r) > JevBodyChars {
		body = string(r[:JevBodyChars])
	}
	return fmt.Sprintf("From: %s\nSubject: %s\nBody: %s", msg.From, msg.Subject, body)
}

func jevQuestions() map[string]jev.Question {
	canonical := labels.Default.Canonical()
	opts := make([]jev.Option, 0, len(canonical))
	for _, name := range canonical {
		opts = append(opts, jev.Option{Key: name, Description: labels.Default.Describe(name)})
	}
	return map[string]jev.Question{
		jevMailingListKey: {Type: jev.KindNoul, Instructions: jevMailingListQuestion},
		jevCategoryKey:    {Type: jev.KindChoice, Instructions: "Which category best fits this email?", Options: opts},
	}
}

// jevResult maps decision answers onto the classify Result shape.
// Confidence is the top category's probability.
func jevResult(answers map[string]jev.Answer) *Result {
	bulk := answers[jevMailingListKey].Noul
	ranked := rankProbabilities(answers[jevCategoryKey].Probabilities)

	res := &Result{IsMailingList: bulk >= 0.5}
	var why []string
	for i, p := range ranked {
		if i > 1 || (i == 1 && p.prob < jevSecondLabelMin) {
			break
		}
		res.SuggestedLabels = append(res.SuggestedLabels, p.key)
		why = append(why, fmt.Sprintf("%s p=%.2f", p.key, p.prob))
	}
	if len(ranked) > 0 {
		res.Confidence = ranked[0].prob
	}
	res.Rationale = fmt.Sprintf("jev: %s; bulk p=%.2f", strings.Join(why, ", "), bulk)
	return res
}

type keyProb struct {
	key  string
	prob float64
}

// rankProbabilities sorts options by descending probability, breaking ties
// by key so results are deterministic.
func rankProbabilities(m map[string]float64) []keyProb {
	out := make([]keyProb, 0, len(m))
	for k, p := range m {
		out = append(out, keyProb{k, p})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].prob != out[j].prob {
			return out[i].prob > out[j].prob
		}
		return out[i].key < out[j].key
	})
	return out
}
