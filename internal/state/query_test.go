package state

import (
	"path/filepath"
	"testing"
	"time"
)

func openQueryTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedMsg(t *testing.T, db *DB, mailbox string, uid uint32, isML bool, conf float64, labels []string, applied bool, applyErr string) {
	t.Helper()
	err := db.MarkProcessed(&ProcessedMessage{
		Mailbox:         mailbox,
		UIDValidity:     100,
		UID:             uid,
		Subject:         "subj",
		From:            "sender@example.com",
		Date:            time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		SuggestedLabels: labels,
		Confidence:      conf,
		Rationale:       "because",
		IsMailingList:   isML,
		LabelsApplied:   applied,
		ApplyError:      applyErr,
		ProcessedAt:     time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("seed uid=%d: %v", uid, err)
	}
}

// seedQueryFixture populates:
//
//	INBOX   uid 1: mailing list, conf 0.9,  ["Newsletters"],        applied
//	INBOX   uid 2: personal,     conf 0.4,  ["Personal"],           not applied
//	INBOX   uid 3: personal,     conf 0.95, ["Personal","Finance"], apply error "boom"
//	Archive uid 9: mailing list, conf 0.7,  ["Newsletters"],        applied
func seedQueryFixture(t *testing.T, db *DB) {
	t.Helper()
	seedMsg(t, db, "INBOX", 1, true, 0.9, []string{"Newsletters"}, true, "")
	seedMsg(t, db, "INBOX", 2, false, 0.4, []string{"Personal"}, false, "")
	seedMsg(t, db, "INBOX", 3, false, 0.95, []string{"Personal", "Finance"}, false, "boom")
	seedMsg(t, db, "Archive", 9, true, 0.7, []string{"Newsletters"}, true, "")
}

func queryUIDs(t *testing.T, db *DB, f QueryFilter) []uint32 {
	t.Helper()
	msgs, err := db.Query(f)
	if err != nil {
		t.Fatalf("Query(%+v): %v", f, err)
	}
	uids := make([]uint32, 0, len(msgs))
	for _, m := range msgs {
		uids = append(uids, m.UID)
	}
	return uids
}

func wantUIDs(t *testing.T, got, want []uint32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got uids %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got uids %v, want %v", got, want)
		}
	}
}

func boolPtr(b bool) *bool      { return &b }
func f64Ptr(f float64) *float64 { return &f }

func TestQueryAll_OrderedByMailboxThenUID(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	// "Archive" < "INBOX" lexically.
	wantUIDs(t, queryUIDs(t, db, QueryFilter{}), []uint32{9, 1, 2, 3})
}

func TestQueryFilterMailbox(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX"}), []uint32{1, 2, 3})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "Archive"}), []uint32{9})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "NoSuch"}), nil)
}

func TestQueryFilterIsMailingList(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", IsMailingList: boolPtr(false)}), []uint32{2, 3})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", IsMailingList: boolPtr(true)}), []uint32{1})
}

func TestQueryFilterConfidence(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", MinConfidence: f64Ptr(0.5)}), []uint32{1, 3})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", MaxConfidence: f64Ptr(0.5)}), []uint32{2})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", MinConfidence: f64Ptr(0.85), MaxConfidence: f64Ptr(0.92)}), []uint32{1})
}

func TestQueryFilterLabel_ExactElementMatch(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Label: "Personal"}), []uint32{2, 3})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Label: "Finance"}), []uint32{3})
	// A prefix of an element must NOT match: label filtering is exact-element.
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Label: "Fin"}), nil)
}

func TestQueryFilterAppliedAndFailed(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Applied: boolPtr(true)}), []uint32{1})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Applied: boolPtr(false)}), []uint32{2, 3})
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Failed: true}), []uint32{3})
}

func TestQueryLimit(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Limit: 2}), []uint32{1, 2})
	// Limit 0 means no limit.
	wantUIDs(t, queryUIDs(t, db, QueryFilter{Mailbox: "INBOX", Limit: 0}), []uint32{1, 2, 3})
}

func TestQueryCombinedFilters(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	wantUIDs(t, queryUIDs(t, db, QueryFilter{
		Mailbox:       "INBOX",
		IsMailingList: boolPtr(false),
		MinConfidence: f64Ptr(0.5),
		Label:         "Personal",
	}), []uint32{3})
}

func TestQueryRoundtripsFields(t *testing.T) {
	db := openQueryTestDB(t)
	seedQueryFixture(t, db)
	msgs, err := db.Query(QueryFilter{Mailbox: "INBOX", Failed: true})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d msgs, want 1", len(msgs))
	}
	m := msgs[0]
	if m.UID != 3 || m.Mailbox != "INBOX" || m.UIDValidity != 100 {
		t.Errorf("identity fields: %+v", m)
	}
	if m.Subject != "subj" || m.From != "sender@example.com" {
		t.Errorf("header fields: %+v", m)
	}
	if m.Confidence != 0.95 || m.Rationale != "because" || m.IsMailingList {
		t.Errorf("classification fields: %+v", m)
	}
	if len(m.SuggestedLabels) != 2 || m.SuggestedLabels[0] != "Personal" || m.SuggestedLabels[1] != "Finance" {
		t.Errorf("labels: %v", m.SuggestedLabels)
	}
	if m.LabelsApplied || m.ApplyError != "boom" {
		t.Errorf("apply fields: %+v", m)
	}
	wantDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !m.Date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", m.Date, wantDate)
	}
	wantProcessed := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if !m.ProcessedAt.Equal(wantProcessed) {
		t.Errorf("processed_at = %v, want %v", m.ProcessedAt, wantProcessed)
	}
}

func TestQueryEmptyDB(t *testing.T) {
	db := openQueryTestDB(t)
	msgs, err := db.Query(QueryFilter{})
	if err != nil {
		t.Fatalf("Query on empty DB: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d msgs, want 0", len(msgs))
	}
}
