package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-imap/client"

	"github.com/higgscli/higgs/internal/imaptest"
	"github.com/higgscli/higgs/internal/state"
)

// mustDial is a minimal Dial+Login helper for tests that need direct IMAP
// access without going through the production imapclient package.
func mustDial(srv *imaptest.Server) (*client.Client, error) {
	cfg := imaptest.Config(srv)
	c, err := client.Dial(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return nil, err
	}
	if err := c.Login(cfg.Username, cfg.Password); err != nil {
		return nil, err
	}
	return c, nil
}

// setIMAPEnv wires env vars so config.LoadFromEnv returns a config pointing at
// the test harness.
func setIMAPEnv(t *testing.T, srv *imaptest.Server) {
	t.Helper()
	cfg := imaptest.Config(srv)
	t.Setenv("PM_IMAP_HOST", cfg.Host)
	t.Setenv("PM_IMAP_PORT", fmt.Sprintf("%d", cfg.Port))
	t.Setenv("PM_IMAP_USERNAME", cfg.Username)
	t.Setenv("PM_IMAP_PASSWORD", cfg.Password)
	t.Setenv("PM_IMAP_SECURITY", string(cfg.Security))
	t.Setenv("PM_IMAP_TLS_SKIP_VERIFY", "true")
}

func rfc822(subject, from string) []byte {
	return []byte(
		"From: " + from + "\r\n" +
			"To: user@example.com\r\n" +
			"Subject: " + subject + "\r\n" +
			"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
			"Message-ID: <" + subject + "@test>\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"This is the body for " + subject + ".\r\n")
}

// ----- scan-folders -----

func TestIntegration_ScanFolders_Happy(t *testing.T) {
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Labels", nil),
		imaptest.WithMailbox("Folders/Accounts", nil),
	)
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdScanFolders() })
	if err != nil {
		t.Fatalf("cmdScanFolders: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if obj["labels_root"] != "Labels" {
		t.Errorf("labels_root = %v, want Labels", obj["labels_root"])
	}
	mboxes, ok := obj["mailboxes"].([]any)
	if !ok || len(mboxes) == 0 {
		t.Fatalf("mailboxes missing/empty: %v", obj["mailboxes"])
	}
}

func TestIntegration_ScanFolders_AuthError(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)
	t.Setenv("PM_IMAP_PASSWORD", "nope")

	_, err := captureStdout(t, func() error { return cmdScanFolders() })
	if err == nil {
		t.Fatal("expected auth error for bad password")
	}
}

// ----- fetch-and-parse -----

func TestIntegration_FetchAndParse_Happy(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("hello", "a@x.com")},
		{RFC822: rfc822("world", "b@x.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", msgs))
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdFetchAndParse("INBOX") })
	if err != nil {
		t.Fatalf("cmdFetchAndParse: %v", err)
	}
	// Output is NDJSON, one summary at the end.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 NDJSON lines, got %d:\n%s", len(lines), stdout)
	}
	// Ensure the last line is the summary.
	var summary map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
		t.Fatalf("summary unmarshal: %v\n%s", err, lines[len(lines)-1])
	}
	if summary["type"] != "summary" {
		t.Errorf("last line is not a summary: %v", summary)
	}
}

func TestIntegration_FetchAndParse_EmptyMailbox(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("Empty", nil))
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdFetchAndParse("Empty") })
	if err != nil {
		t.Fatalf("cmdFetchAndParse empty: %v", err)
	}
	if !strings.Contains(stdout, `"fetched":0`) {
		t.Errorf("expected fetched=0; got:\n%s", stdout)
	}
}

func TestIntegration_FetchAndParse_Truncates(t *testing.T) {
	// Seed more than maxFetch (5) messages; fetch-and-parse caps to the last 5.
	seed := []imaptest.Message{}
	for i := 0; i < 8; i++ {
		seed = append(seed, imaptest.Message{RFC822: rfc822(fmt.Sprintf("m%d", i), "a@b.com")})
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", seed))
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdFetchAndParse("INBOX") })
	if err != nil {
		t.Fatalf("cmdFetchAndParse truncate: %v", err)
	}
	// The summary's fetched=5 confirms the truncation ran.
	if !strings.Contains(stdout, `"fetched":5`) {
		t.Errorf("expected fetched=5 after truncation; got:\n%s", stdout)
	}
}

func TestIntegration_FetchAndParse_AuthError(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)
	t.Setenv("PM_IMAP_PASSWORD", "nope")

	if _, err := captureStdout(t, func() error { return cmdFetchAndParse("INBOX") }); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestIntegration_ApplyLabels_AuthError(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)
	t.Setenv("PM_IMAP_PASSWORD", "nope")
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))

	if _, err := captureStdout(t, func() error { return cmdApplyLabels("Folders/Accounts", 0, false) }); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestIntegration_CleanupLabels_AuthError(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)
	t.Setenv("PM_IMAP_PASSWORD", "nope")

	if _, err := captureStdout(t, func() error { return cmdCleanupLabels(false) }); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestIntegration_Classify_AuthError(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)
	t.Setenv("PM_IMAP_PASSWORD", "nope")

	if _, err := captureStdout(t, func() error {
		return cmdClassify("INBOX", false, false, 0, true, false, 1, 0, false)
	}); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestIntegration_FetchAndParse_BadMailbox(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)

	_, err := captureStdout(t, func() error { return cmdFetchAndParse("NoSuchMailbox") })
	if err == nil {
		t.Fatal("expected validation error for missing mailbox")
	}
}

// ----- apply-labels -----

func TestIntegration_ApplyLabels_DryRun_NoUnapplied(t *testing.T) {
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", nil),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))

	stdout, err := captureStdout(t, func() error { return cmdApplyLabels("Folders/Accounts", 0, true) })
	if err != nil {
		t.Fatalf("cmdApplyLabels: %v", err)
	}
	// Last line is the summary, applied=0.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var summary map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if summary["type"] != "summary" {
		t.Errorf("summary missing: %v", summary)
	}
}

func TestIntegration_ApplyLabels_DryRun_WithSeeded(t *testing.T) {
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", []imaptest.Message{{RFC822: rfc822("seed", "x@x.com")}}),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", dbPath)

	// Pre-seed the state DB with a classification for that message so
	// apply-labels --dry-run has something to print.
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	// Figure out UIDVALIDITY by connecting with cmdFetchAndParse flow — simpler
	// to call Select directly.
	cfg := imaptest.Config(srv)
	_ = cfg
	// Just use UIDVALIDITY=1; state.MarkProcessed accepts arbitrary values,
	// and the command will call Select and read whatever the server reports.
	// So instead seed with the actual UIDVALIDITY after connecting.
	// Small helper: dial, select, get UIDVALIDITY.
	uv := func() uint32 {
		c, err := mustDial(srv)
		if err != nil {
			t.Fatalf("dial for uv: %v", err)
		}
		defer c.Logout()
		st, err := c.Select("Folders/Accounts", true)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		return st.UidValidity
	}()
	if err := db.MarkProcessed(&state.ProcessedMessage{
		Mailbox:         "Folders/Accounts",
		UIDValidity:     uv,
		UID:             1,
		Subject:         "seed",
		From:            "x@x.com",
		SuggestedLabels: []string{"Finance"},
	}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	db.Close()

	stdout, err := captureStdout(t, func() error { return cmdApplyLabels("Folders/Accounts", 0, true) })
	if err != nil {
		t.Fatalf("cmdApplyLabels: %v", err)
	}
	if !strings.Contains(stdout, `"pending"`) {
		t.Errorf("expected a pending row in dry-run output; got:\n%s", stdout)
	}
}

func TestIntegration_ApplyLabels_BadMailbox(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	if _, err := captureStdout(t, func() error { return cmdApplyLabels("NoSuch", 0, false) }); err == nil {
		t.Fatal("expected error for missing mailbox")
	}
}

// ----- cleanup-labels -----

func TestIntegration_CleanupLabels_NoWork(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdCleanupLabels(false) })
	if err != nil {
		t.Fatalf("cmdCleanupLabels: %v", err)
	}
	// Summary with processed=0.
	var summary map[string]any
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary["processed"].(float64) != 0 {
		t.Errorf("processed = %v, want 0", summary["processed"])
	}
}

func TestIntegration_CleanupLabels_DryRun_WithNonCanonical(t *testing.T) {
	// Seed an old-style label folder "Labels/shipping" which is an alias of
	// "Orders". In --dry-run mode the command prints one pending row per label.
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Labels", nil),
		imaptest.WithMailbox("Labels/shipping", nil),
	)
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdCleanupLabels(true) })
	if err != nil {
		t.Fatalf("cmdCleanupLabels: %v", err)
	}
	if !strings.Contains(stdout, "Labels/shipping") {
		t.Errorf("expected Labels/shipping in dry-run output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Labels/Orders") {
		t.Errorf("expected Labels/Orders (canonical) in dry-run output:\n%s", stdout)
	}
}

// ----- cleanup-labels real run -----

func TestIntegration_CleanupLabels_RealRun_EmptyAlias(t *testing.T) {
	// An empty Labels/shipping should be deleted outright (no messages to
	// move); no Labels/Orders target needed beyond creation.
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Labels", nil),
		imaptest.WithMailbox("Labels/shipping", nil),
	)
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdCleanupLabels(false) })
	if err != nil {
		t.Fatalf("cmdCleanupLabels: %v", err)
	}
	if !strings.Contains(stdout, "Labels/shipping") {
		t.Errorf("expected Labels/shipping in output:\n%s", stdout)
	}
	// Summary last line.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var summary map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary["processed"].(float64) == 0 {
		t.Errorf("expected processed>0, got %v", summary["processed"])
	}
}

func TestIntegration_CleanupLabels_RealRun_WithMessages(t *testing.T) {
	// A Labels/shipping with seeded messages triggers the COPY + STORE + EXPUNGE path.
	msgs := []imaptest.Message{
		{RFC822: rfc822("hi", "a@b.com")},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Labels", nil),
		imaptest.WithMailbox("Labels/shipping", msgs),
	)
	setIMAPEnv(t, srv)

	stdout, err := captureStdout(t, func() error { return cmdCleanupLabels(false) })
	if err != nil {
		t.Fatalf("cmdCleanupLabels: %v", err)
	}
	// Expect the messages_moved field to be >=1 in the per-label row.
	if !strings.Contains(stdout, `"messages_moved":1`) && !strings.Contains(stdout, `"messages_moved":2`) {
		t.Errorf("expected messages_moved in output:\n%s", stdout)
	}
}

func TestIntegration_CleanupLabels_UnmappableLabel(t *testing.T) {
	// Labels/xyz has no canonical mapping; the command should warn + skip.
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Labels", nil),
		imaptest.WithMailbox("Labels/xyz", nil),
	)
	setIMAPEnv(t, srv)
	// With no mappable labels, cleanup goes through the "no labels need cleanup"
	// branch.
	stdout, err := captureStdout(t, func() error { return cmdCleanupLabels(false) })
	if err != nil {
		t.Fatalf("cmdCleanupLabels: %v", err)
	}
	if !strings.Contains(stdout, `"processed":0`) {
		t.Errorf("expected processed=0 summary; got:\n%s", stdout)
	}
}

// ----- apply-labels real run -----

func TestIntegration_ApplyLabels_RealRun_AppliesFromState(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("one", "a@b.com")},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", msgs),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", dbPath)

	// Seed state DB: pretend classification assigned Finance.
	uv := func() uint32 {
		c, err := mustDial(srv)
		if err != nil {
			t.Fatalf("dial uv: %v", err)
		}
		defer c.Logout()
		st, err := c.Select("Folders/Accounts", true)
		if err != nil {
			t.Fatalf("select uv: %v", err)
		}
		return st.UidValidity
	}()
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	if err := db.MarkProcessed(&state.ProcessedMessage{
		Mailbox:         "Folders/Accounts",
		UIDValidity:     uv,
		UID:             1,
		Subject:         "one",
		From:            "a@b.com",
		SuggestedLabels: []string{"Finance"},
	}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	db.Close()

	stdout, err := captureStdout(t, func() error { return cmdApplyLabels("Folders/Accounts", 0, false) })
	if err != nil {
		t.Fatalf("cmdApplyLabels: %v", err)
	}
	if !strings.Contains(stdout, `"applied":true`) {
		t.Errorf("expected applied=true row:\n%s", stdout)
	}
}

func TestIntegration_ApplyLabels_RealRun_EmptyLabels(t *testing.T) {
	// Message exists in state but has no SuggestedLabels — should still mark applied.
	msgs := []imaptest.Message{
		{RFC822: rfc822("empty", "a@b.com")},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", msgs),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", dbPath)

	uv := func() uint32 {
		c, err := mustDial(srv)
		if err != nil {
			t.Fatalf("dial uv: %v", err)
		}
		defer c.Logout()
		st, err := c.Select("Folders/Accounts", true)
		if err != nil {
			t.Fatalf("select uv: %v", err)
		}
		return st.UidValidity
	}()
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	if err := db.MarkProcessed(&state.ProcessedMessage{
		Mailbox:         "Folders/Accounts",
		UIDValidity:     uv,
		UID:             1,
		Subject:         "empty",
		From:            "a@b.com",
		SuggestedLabels: nil,
	}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	db.Close()

	stdout, err := captureStdout(t, func() error { return cmdApplyLabels("Folders/Accounts", 0, false) })
	if err != nil {
		t.Fatalf("cmdApplyLabels: %v", err)
	}
	if !strings.Contains(stdout, `"applied":true`) {
		t.Errorf("expected applied=true row:\n%s", stdout)
	}
}

// ----- state stats with data -----

func TestIntegration_StateStats_WithData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", dbPath)
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	if err := db.MarkProcessed(&state.ProcessedMessage{
		Mailbox:     "Folders/Accounts",
		UIDValidity: 1,
		UID:         1,
		SuggestedLabels: []string{"Finance"},
	}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	db.Close()

	stdout, err := captureStdout(t, func() error { return cmdStateStats("") })
	if err != nil {
		t.Fatalf("cmdStateStats: %v", err)
	}
	if !strings.Contains(stdout, "Folders/Accounts") {
		t.Errorf("expected mailbox listed:\n%s", stdout)
	}
}

// ----- classify -----

// fakeOllama stands up an httptest.Server that replies with a fixed structured
// classification payload.
func fakeOllama(t *testing.T, labels []string) *httptest.Server {
	t.Helper()
	type inner struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type resp struct {
		Message inner `json:"message"`
		Done    bool  `json:"done"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		classification := map[string]any{
			"suggested_labels": labels,
			"confidence":       0.9,
			"rationale":        "test",
			"is_mailing_list":  false,
		}
		b, _ := json.Marshal(classification)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp{Message: inner{Role: "assistant", Content: string(b)}, Done: true})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIntegration_Classify_DryRun_NoState(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("promo", "shop@store.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)

	ollama := fakeOllama(t, []string{"Promotions"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "test-model")

	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", true, false, 0, true, false, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
	if !strings.Contains(stdout, `"Promotions"`) {
		t.Errorf("expected Promotions label in output:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"dry_run":true`) {
		t.Errorf("expected dry_run flag in output:\n%s", stdout)
	}
}

func TestIntegration_Classify_Apply_CreatesLabels(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("order", "orders@amazon.com")},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", msgs),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))

	ollama := fakeOllama(t, []string{"Orders"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "test-model")

	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, true, 0, false, false, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
	if !strings.Contains(stdout, `"labels_applied":true`) {
		t.Errorf("expected labels_applied=true in output:\n%s", stdout)
	}
}

func TestIntegration_Classify_EmptyMailbox(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("Empty", nil))
	setIMAPEnv(t, srv)

	// Ollama isn't called because there are no UIDs.
	ollama := fakeOllama(t, nil)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Empty", false, false, 0, true, false, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
	if !strings.Contains(stdout, `"classified":0`) {
		t.Errorf("expected classified=0 for empty mailbox:\n%s", stdout)
	}
}

func TestIntegration_Classify_ResolvesMailboxName(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("hi", "a@x.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)

	ollama := fakeOllama(t, []string{"Finance"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	// Lowercase — will be resolved to the canonical name.
	_, err := captureStdout(t, func() error {
		return cmdClassify("folders/accounts", true, false, 0, true, false, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("cmdClassify resolve: %v", err)
	}
}

// TestIntegration_Classify_OllamaError exercises the classify-error path (the
// per-message error row is written to stdout with an error envelope).
func TestIntegration_Classify_OllamaError(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("x", "a@b.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)

	// Ollama server that always returns 500.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	t.Setenv("PM_OLLAMA_BASE_URL", bad.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", true, false, 0, true, false, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
	if !strings.Contains(stdout, `"errors":1`) {
		t.Errorf("expected errors=1 in summary; got:\n%s", stdout)
	}
}

// TestIntegration_Classify_WithStateDB exercises the state-DB reprocess
// filtering path. Run classify twice; the second run should skip.
func TestIntegration_Classify_SkipsProcessed(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("x", "a@b.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))

	ollama := fakeOllama(t, []string{"Finance"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	// First run populates the state DB.
	if _, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, false, 0, false, false, 1, 0, false)
	}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Second run without --reprocess should see skipped>0.
	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, false, 0, false, false, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	// Second run's summary should report classified=0 skipped=1.
	if !strings.Contains(stdout, `"classified":0`) {
		t.Errorf("expected classified=0 on second run; got:\n%s", stdout)
	}
}

// TestIntegration_ApplyLabels_LimitFlag exercises the limit parameter.
func TestIntegration_ApplyLabels_LimitFlag(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("one", "a@b.com")},
		{RFC822: rfc822("two", "b@b.com")},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", msgs),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", dbPath)

	uv := func() uint32 {
		c, err := mustDial(srv)
		if err != nil {
			t.Fatalf("dial uv: %v", err)
		}
		defer c.Logout()
		st, err := c.Select("Folders/Accounts", true)
		if err != nil {
			t.Fatalf("select uv: %v", err)
		}
		return st.UidValidity
	}()
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	for _, uid := range []uint32{1, 2} {
		if err := db.MarkProcessed(&state.ProcessedMessage{
			Mailbox:         "Folders/Accounts",
			UIDValidity:     uv,
			UID:             uid,
			SuggestedLabels: []string{"Finance"},
		}); err != nil {
			t.Fatalf("MarkProcessed: %v", err)
		}
	}
	db.Close()

	// limit=1 should process exactly one.
	stdout, err := captureStdout(t, func() error { return cmdApplyLabels("Folders/Accounts", 1, false) })
	if err != nil {
		t.Fatalf("cmdApplyLabels: %v", err)
	}
	if !strings.Contains(stdout, `"applied":1`) {
		t.Errorf("expected applied=1 summary; got:\n%s", stdout)
	}
}

// TestIntegration_ApplyLabels_ApplyFails triggers the failed-row path by
// pre-creating a label mailbox with a name that the command will later try to
// create — but the memory backend allows CREATE of already-existing mailboxes
// to succeed via the existing-set cache, so the failure has to come from
// SELECT (no such source). We point apply-labels at a mailbox that exists in
// state but is missing on the server, forcing a validation error.
// TestIntegration_Classify_LimitFlag and workers>1 exercise the parameter
// precedence path and worker-count normalization.
func TestIntegration_Classify_LimitFlag_MultiWorker(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("a", "a@b.com")},
		{RFC822: rfc822("b", "b@b.com")},
		{RFC822: rfc822("c", "c@b.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)

	ollama := fakeOllama(t, []string{"Finance"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, false, 2, true, false, 2, 0, false)
	})
	if err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
	if !strings.Contains(stdout, `"classified":2`) {
		t.Errorf("expected classified=2 with limit=2; got:\n%s", stdout)
	}
	// Default (newest-first): with 3 messages and limit=2 we should see "b" and
	// "c" (the two highest UIDs), NOT "a" (the oldest).
	if strings.Contains(stdout, `"subject":"a"`) {
		t.Errorf("--limit should default to newest UIDs, but oldest (subject=a) was classified:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"subject":"b"`) || !strings.Contains(stdout, `"subject":"c"`) {
		t.Errorf("expected newest two (b,c) to be classified; got:\n%s", stdout)
	}
}

func TestIntegration_Classify_LimitFlag_OldestFirst(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("a", "a@b.com")},
		{RFC822: rfc822("b", "b@b.com")},
		{RFC822: rfc822("c", "c@b.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)

	ollama := fakeOllama(t, []string{"Finance"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, false, 2, true, false, 2, 0, true)
	})
	if err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
	if !strings.Contains(stdout, `"classified":2`) {
		t.Errorf("expected classified=2; got:\n%s", stdout)
	}
	// --oldest-first: with 3 messages and limit=2 we expect "a" and "b", NOT "c".
	if strings.Contains(stdout, `"subject":"c"`) {
		t.Errorf("--oldest-first should skip the newest (subject=c); got:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"subject":"a"`) || !strings.Contains(stdout, `"subject":"b"`) {
		t.Errorf("expected oldest two (a,b) to be classified; got:\n%s", stdout)
	}
}

// TestIntegration_Classify_Apply_NoLabels exercises the apply-mode branch
// where the AI returns no suggested labels (nothing to apply, but the row
// still flows through the apply summary accounting).
func TestIntegration_Classify_Apply_NoLabels(t *testing.T) {
	msgs := []imaptest.Message{{RFC822: rfc822("x", "a@b.com")}}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", msgs),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))

	ollama := fakeOllama(t, []string{}) // empty slice
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, true, 0, false, false, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
	// With no labels, labels_applied should still be set (false, since apply
	// short-circuits when len(SuggestedLabels)==0).
	if !strings.Contains(stdout, `"applied":0`) {
		t.Errorf("expected applied=0 summary; got:\n%s", stdout)
	}
}

// TestIntegration_Classify_ZeroApplyTimeout exercises the
// applyTimeout<=0 guard path (default to 180s).
func TestIntegration_Classify_ZeroApplyTimeout(t *testing.T) {
	msgs := []imaptest.Message{{RFC822: rfc822("x", "a@b.com")}}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", msgs),
		imaptest.WithMailbox("Labels", nil),
	)
	setIMAPEnv(t, srv)
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	t.Setenv("PM_IMAP_APPLY_TIMEOUT", "0")

	ollama := fakeOllama(t, []string{"Finance"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	if _, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, true, 0, false, false, 1, 0, false)
	}); err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
}

// TestIntegration_Classify_Reprocess exercises the --reprocess path.
func TestIntegration_Classify_Reprocess(t *testing.T) {
	msgs := []imaptest.Message{{RFC822: rfc822("x", "a@b.com")}}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))

	ollama := fakeOllama(t, []string{"Finance"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	// First run.
	if _, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, false, 0, false, false, 1, 0, false)
	}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Second with reprocess=true.
	stdout, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, false, 0, false, true, 1, 0, false)
	})
	if err != nil {
		t.Fatalf("reprocess run: %v", err)
	}
	if !strings.Contains(stdout, `"classified":1`) {
		t.Errorf("expected classified=1 on reprocess; got:\n%s", stdout)
	}
}

// TestIntegration_Classify_InvalidWorkers ensures zero/negative workers is
// normalized to 1 and classification still succeeds.
func TestIntegration_Classify_ZeroWorkers(t *testing.T) {
	msgs := []imaptest.Message{{RFC822: rfc822("x", "a@b.com")}}
	srv := imaptest.Start(t, imaptest.WithMailbox("Folders/Accounts", msgs))
	setIMAPEnv(t, srv)
	t.Setenv("PM_CLASSIFY_WORKERS", "-1")

	ollama := fakeOllama(t, []string{"Finance"})
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	if _, err := captureStdout(t, func() error {
		return cmdClassify("Folders/Accounts", false, false, 0, true, false, 0, 0, false)
	}); err != nil {
		t.Fatalf("cmdClassify: %v", err)
	}
}

func TestIntegration_ApplyLabels_StaleState(t *testing.T) {
	// Server has no Folders/Accounts but state DB references it.
	srv := imaptest.Start(t, imaptest.WithMailbox("Labels", nil))
	setIMAPEnv(t, srv)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", dbPath)

	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	if err := db.MarkProcessed(&state.ProcessedMessage{
		Mailbox:         "Folders/Accounts",
		UIDValidity:     1,
		UID:             99,
		SuggestedLabels: []string{"Finance"},
	}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	db.Close()

	// cmdApplyLabels should return a validation error because the mailbox
	// doesn't exist on the server (LIST + ResolveMailboxName fails).
	_, err = captureStdout(t, func() error { return cmdApplyLabels("Folders/Accounts", 0, false) })
	if err == nil {
		t.Fatal("expected validation error for missing source mailbox")
	}
}

func TestIntegration_Classify_BadMailbox(t *testing.T) {
	srv := imaptest.Start(t)
	setIMAPEnv(t, srv)

	ollama := fakeOllama(t, nil)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)

	_, err := captureStdout(t, func() error {
		return cmdClassify("NoSuch", true, false, 0, true, false, 1, 0, false)
	})
	if err == nil {
		t.Fatal("expected validation error for missing mailbox")
	}
}
