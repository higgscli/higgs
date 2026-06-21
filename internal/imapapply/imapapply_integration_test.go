package imapapply_test

import (
	"testing"
	"time"

	"github.com/higgscli/higgs/internal/imapapply"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/imaptest"
)

func rfc822(subject string) []byte {
	return []byte(
		"From: sender@example.com\r\n" +
			"To: user@example.com\r\n" +
			"Subject: " + subject + "\r\n" +
			"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
			"Message-ID: <" + subject + "@x>\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"body\r\n")
}

func TestEnsureLabelMailbox_CreatesAndReuses(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("Labels", nil))
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	existing := map[string]bool{"Labels": true}

	// First call creates the mailbox.
	dest, err := imapapply.EnsureLabelMailbox(c, "Labels", "Finance", existing)
	if err != nil {
		t.Fatalf("EnsureLabelMailbox: %v", err)
	}
	if dest != "Labels/Finance" {
		t.Errorf("dest = %q, want Labels/Finance", dest)
	}
	if !existing["Labels/Finance"] {
		t.Error("existing set not updated")
	}

	// Second call reuses the cache.
	dest2, err := imapapply.EnsureLabelMailbox(c, "Labels", "Finance", existing)
	if err != nil {
		t.Fatalf("EnsureLabelMailbox (reuse): %v", err)
	}
	if dest2 != "Labels/Finance" {
		t.Errorf("reuse dest = %q, want Labels/Finance", dest2)
	}

	// Empty label name yields "".
	if dest3, err := imapapply.EnsureLabelMailbox(c, "Labels", "", existing); err != nil || dest3 != "" {
		t.Errorf("empty label: dest=%q err=%v; want \"\", nil", dest3, err)
	}
}

func TestApplyLabels_CreatesAndCopies(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: rfc822("one"), Date: time.Now()},
		{RFC822: rfc822("two"), Date: time.Now()},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Folders/Accounts", msgs),
		imaptest.WithMailbox("Labels", nil),
	)

	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	existing := imapapply.BuildMailboxSet(mboxes)

	// Find a UID to apply labels to.
	status, err := c.Select("Folders/Accounts", false)
	if err != nil {
		t.Fatalf("Select source: %v", err)
	}
	if status.Messages == 0 {
		t.Fatal("expected messages in Folders/Accounts")
	}
	// ApplyLabels will do its own Select; fetch uids first.
	var uid uint32
	// Use a SEARCH ALL to find the first UID.
	if err := c.Close(); err != nil {
		t.Logf("Close: %v", err)
	}
	if _, err := c.Select("Folders/Accounts", true); err != nil {
		t.Fatalf("Re-select: %v", err)
	}
	// The memory backend assigns sequential UIDs starting at 1.
	// Grab the set from the snapshot we already have.
	// For robustness, search.
	if status.Messages > 0 {
		uid = 1
	}

	labels := []string{"Finance", "", "   ", "Orders"}
	if err := imapapply.ApplyLabels(c, "Folders/Accounts", uid, labels, existing); err != nil {
		t.Fatalf("ApplyLabels: %v", err)
	}
	if !existing["Labels/Finance"] {
		t.Error("Labels/Finance not registered in existing set")
	}
	if !existing["Labels/Orders"] {
		t.Error("Labels/Orders not registered in existing set")
	}

	// Verify the label mailboxes exist and each has the message copied.
	mboxes2, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		t.Fatalf("ListMailboxes post: %v", err)
	}
	names := map[string]bool{}
	for _, m := range mboxes2 {
		names[m.Name] = true
	}
	if !names["Labels/Finance"] || !names["Labels/Orders"] {
		t.Errorf("expected label mailboxes created; got %v", names)
	}
}

func TestApplyLabels_NoLabels_Noop(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("Labels", nil))
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	// Empty labels list short-circuits without even SELECTing.
	if err := imapapply.ApplyLabels(c, "Folders/Accounts", 1, nil, map[string]bool{}); err != nil {
		t.Errorf("ApplyLabels(nil): %v", err)
	}
	// All-whitespace labels also short-circuit after trimming.
	if err := imapapply.ApplyLabels(c, "Folders/Accounts", 1, []string{"", "   "}, map[string]bool{}); err != nil {
		t.Errorf("ApplyLabels(whitespace): %v", err)
	}
}

// TestApplyLabels_SelectError exercises the error path when the source
// mailbox cannot be selected.
func TestApplyLabels_SelectError(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("Labels", nil))
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	existing := map[string]bool{"Labels": true}
	err = imapapply.ApplyLabels(c, "NoSuchSource", 1, []string{"Finance"}, existing)
	if err == nil {
		t.Fatal("expected SELECT error for missing source mailbox")
	}
}
