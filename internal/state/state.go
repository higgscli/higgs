// Package state provides SQLite-based state tracking for idempotent email classification.
package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database for tracking processed messages.
type DB struct {
	db *sql.DB
}

// ProcessedMessage represents a message that has been classified.
type ProcessedMessage struct {
	Mailbox        string    `json:"mailbox"`
	UIDValidity    uint32    `json:"uid_validity"`
	UID            uint32    `json:"uid"`
	Subject        string    `json:"subject"`
	From           string    `json:"from"`
	Date           time.Time `json:"date"`
	SuggestedLabels []string `json:"suggested_labels"`
	Confidence     float64   `json:"confidence"`
	Rationale      string    `json:"rationale"`
	IsMailingList  bool      `json:"is_mailing_list"`
	LabelsApplied  bool      `json:"labels_applied"`
	ApplyError     string    `json:"apply_error,omitempty"`
	ProcessedAt    time.Time `json:"processed_at"`
}

// Open opens or creates a SQLite database at the given path.
// If path is empty, uses ~/.protoncli/state.db
func Open(path string) (*DB, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		dir := filepath.Join(home, ".protoncli")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create state dir: %w", err)
		}
		path = filepath.Join(dir, "state.db")
	}

	// Configure a busy timeout so concurrent writers wait instead of failing
	// with SQLITE_BUSY. modernc.org/sqlite accepts PRAGMAs via the DSN.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Create tables
	if err := createTables(db); err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db: db}, nil
}

func createTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS processed_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox TEXT NOT NULL,
			uid_validity INTEGER NOT NULL,
			uid INTEGER NOT NULL,
			subject TEXT,
			sender TEXT,
			date TEXT,
			suggested_labels TEXT,
			confidence REAL,
			rationale TEXT,
			is_mailing_list INTEGER,
			labels_applied INTEGER NOT NULL DEFAULT 0,
			apply_error TEXT,
			processed_at TEXT NOT NULL,
			UNIQUE(mailbox, uid_validity, uid)
		);
		
		CREATE INDEX IF NOT EXISTS idx_processed_mailbox_validity 
			ON processed_messages(mailbox, uid_validity);
	`)
	return err
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// IsProcessed checks if a message has already been processed.
func (d *DB) IsProcessed(mailbox string, uidValidity, uid uint32) (bool, error) {
	var count int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM processed_messages 
		WHERE mailbox = ? AND uid_validity = ? AND uid = ?
	`, mailbox, uidValidity, uid).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check processed: %w", err)
	}
	return count > 0, nil
}

// MarkProcessed records that a message has been processed.
func (d *DB) MarkProcessed(msg *ProcessedMessage) error {
	labelsJSON, err := json.Marshal(msg.SuggestedLabels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}

	labelsApplied := 0
	if msg.LabelsApplied {
		labelsApplied = 1
	}
	isMailingList := 0
	if msg.IsMailingList {
		isMailingList = 1
	}

	_, err = d.db.Exec(`
		INSERT INTO processed_messages 
			(mailbox, uid_validity, uid, subject, sender, date, suggested_labels, 
			 confidence, rationale, is_mailing_list, labels_applied, apply_error, processed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mailbox, uid_validity, uid) DO UPDATE SET
			suggested_labels = excluded.suggested_labels,
			confidence = excluded.confidence,
			rationale = excluded.rationale,
			is_mailing_list = excluded.is_mailing_list,
			labels_applied = excluded.labels_applied,
			apply_error = excluded.apply_error,
			processed_at = excluded.processed_at
	`, msg.Mailbox, msg.UIDValidity, msg.UID, msg.Subject, msg.From,
		msg.Date.Format(time.RFC3339), string(labelsJSON),
		msg.Confidence, msg.Rationale, isMailingList, labelsApplied,
		msg.ApplyError, msg.ProcessedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert processed: %w", err)
	}
	return nil
}

// GetProcessedUIDs returns all processed UIDs for a mailbox with matching UIDVALIDITY.
// This is useful for filtering out already-processed messages before fetching.
func (d *DB) GetProcessedUIDs(mailbox string, uidValidity uint32) (map[uint32]bool, error) {
	rows, err := d.db.Query(`
		SELECT uid FROM processed_messages 
		WHERE mailbox = ? AND uid_validity = ?
	`, mailbox, uidValidity)
	if err != nil {
		return nil, fmt.Errorf("query processed UIDs: %w", err)
	}
	defer rows.Close()

	result := make(map[uint32]bool)
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan uid: %w", err)
		}
		result[uid] = true
	}
	return result, rows.Err()
}

// GetStats returns statistics about processed messages.
// If mailbox is empty, returns stats for all mailboxes.
func (d *DB) GetStats(mailbox string) (total, applied, failed int, err error) {
	var query string
	var args []interface{}
	if mailbox == "" || mailbox == "%" {
		query = `
			SELECT 
				COUNT(*),
				COALESCE(SUM(CASE WHEN labels_applied = 1 THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN labels_applied = 0 AND apply_error != '' THEN 1 ELSE 0 END), 0)
			FROM processed_messages`
	} else {
		query = `
			SELECT 
				COUNT(*),
				COALESCE(SUM(CASE WHEN labels_applied = 1 THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN labels_applied = 0 AND apply_error != '' THEN 1 ELSE 0 END), 0)
			FROM processed_messages 
			WHERE mailbox = ?`
		args = append(args, mailbox)
	}
	err = d.db.QueryRow(query, args...).Scan(&total, &applied, &failed)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("get stats: %w", err)
	}
	return total, applied, failed, nil
}

// ListMailboxes returns all unique mailboxes in the state DB.
func (d *DB) ListMailboxes() ([]string, error) {
	rows, err := d.db.Query(`SELECT DISTINCT mailbox FROM processed_messages ORDER BY mailbox`)
	if err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var mailbox string
		if err := rows.Scan(&mailbox); err != nil {
			return nil, fmt.Errorf("scan mailbox: %w", err)
		}
		result = append(result, mailbox)
	}
	return result, rows.Err()
}

// ClearMailbox removes all processed records for a mailbox.
// Use this when UIDVALIDITY changes (mailbox was recreated).
func (d *DB) ClearMailbox(mailbox string, uidValidity uint32) error {
	_, err := d.db.Exec(`
		DELETE FROM processed_messages 
		WHERE mailbox = ? AND uid_validity != ?
	`, mailbox, uidValidity)
	return err
}

// GetUnappliedMessages returns messages that have been classified but not yet had labels applied.
func (d *DB) GetUnappliedMessages(mailbox string, uidValidity uint32, limit int) ([]*ProcessedMessage, error) {
	query := `
		SELECT mailbox, uid_validity, uid, subject, sender, suggested_labels
		FROM processed_messages 
		WHERE mailbox = ? AND uid_validity = ? AND labels_applied = 0 AND (apply_error = '' OR apply_error IS NULL)
		ORDER BY uid
	`
	if limit > 0 {
		// #nosec G202 -- limit is an int bound, not user-supplied string input.
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := d.db.Query(query, mailbox, uidValidity)
	if err != nil {
		return nil, fmt.Errorf("query unapplied: %w", err)
	}
	defer rows.Close()

	var result []*ProcessedMessage
	for rows.Next() {
		var msg ProcessedMessage
		var labelsJSON string
		if err := rows.Scan(&msg.Mailbox, &msg.UIDValidity, &msg.UID, &msg.Subject, &msg.From, &labelsJSON); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if err := json.Unmarshal([]byte(labelsJSON), &msg.SuggestedLabels); err != nil {
			// Skip messages with invalid labels JSON
			continue
		}
		result = append(result, &msg)
	}
	return result, rows.Err()
}

// MarkLabelsApplied updates a message to indicate labels were applied.
func (d *DB) MarkLabelsApplied(mailbox string, uidValidity, uid uint32, applied bool, applyError string) error {
	labelsApplied := 0
	if applied {
		labelsApplied = 1
	}
	_, err := d.db.Exec(`
		UPDATE processed_messages 
		SET labels_applied = ?, apply_error = ?
		WHERE mailbox = ? AND uid_validity = ? AND uid = ?
	`, labelsApplied, applyError, mailbox, uidValidity, uid)
	return err
}

// CountUnapplied returns the count of messages needing label application.
func (d *DB) CountUnapplied(mailbox string, uidValidity uint32) (int, error) {
	var count int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM processed_messages 
		WHERE mailbox = ? AND uid_validity = ? AND labels_applied = 0 AND (apply_error = '' OR apply_error IS NULL)
	`, mailbox, uidValidity).Scan(&count)
	return count, err
}
