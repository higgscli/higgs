package state

import (
	"encoding/json"
	"fmt"
	"time"
)

// QueryFilter selects processed messages. Zero values mean "no filter":
// pointer fields are only applied when non-nil, and Limit 0 means unlimited.
type QueryFilter struct {
	Mailbox       string   // exact mailbox name; empty = all mailboxes
	IsMailingList *bool    // filter on the mailing-list flag
	MinConfidence *float64 // inclusive lower bound
	MaxConfidence *float64 // inclusive upper bound
	Label         string   // exact element match against suggested labels
	Applied       *bool    // filter on labels_applied
	Failed        bool     // only messages with a non-empty apply_error
	Limit         int      // max rows; 0 = unlimited
}

// Query returns processed messages matching the filter, ordered by mailbox
// then UID.
func (d *DB) Query(f QueryFilter) ([]*ProcessedMessage, error) {
	query := `
		SELECT mailbox, uid_validity, uid, subject, sender, date, suggested_labels,
		       confidence, rationale, is_mailing_list, labels_applied, apply_error, processed_at
		FROM processed_messages
		WHERE 1 = 1`
	var args []interface{}

	if f.Mailbox != "" {
		query += " AND mailbox = ?"
		args = append(args, f.Mailbox)
	}
	if f.IsMailingList != nil {
		isMailingList := 0
		if *f.IsMailingList {
			isMailingList = 1
		}
		query += " AND is_mailing_list = ?"
		args = append(args, isMailingList)
	}
	if f.MinConfidence != nil {
		query += " AND confidence >= ?"
		args = append(args, *f.MinConfidence)
	}
	if f.MaxConfidence != nil {
		query += " AND confidence <= ?"
		args = append(args, *f.MaxConfidence)
	}
	if f.Label != "" {
		// Labels are stored as a JSON array, so matching the label with its
		// surrounding quotes gives exact-element semantics: a prefix like
		// "Fin" cannot match the element "Finance".
		query += " AND suggested_labels LIKE ?"
		args = append(args, `%"`+f.Label+`"%`)
	}
	if f.Applied != nil {
		labelsApplied := 0
		if *f.Applied {
			labelsApplied = 1
		}
		query += " AND labels_applied = ?"
		args = append(args, labelsApplied)
	}
	if f.Failed {
		query += " AND apply_error IS NOT NULL AND apply_error != ''"
	}
	query += " ORDER BY mailbox, uid"
	if f.Limit > 0 {
		// #nosec G202 -- limit is an int bound, not user-supplied string input.
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var result []*ProcessedMessage
	for rows.Next() {
		var msg ProcessedMessage
		var dateStr, labelsJSON, processedAtStr string
		var isMailingList, labelsApplied int
		if err := rows.Scan(&msg.Mailbox, &msg.UIDValidity, &msg.UID, &msg.Subject, &msg.From,
			&dateStr, &labelsJSON, &msg.Confidence, &msg.Rationale,
			&isMailingList, &labelsApplied, &msg.ApplyError, &processedAtStr); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msg.IsMailingList = isMailingList != 0
		msg.LabelsApplied = labelsApplied != 0
		if err := json.Unmarshal([]byte(labelsJSON), &msg.SuggestedLabels); err != nil {
			// Skip messages with invalid labels JSON
			continue
		}
		if dateStr != "" {
			if msg.Date, err = time.Parse(time.RFC3339, dateStr); err != nil {
				return nil, fmt.Errorf("parse date: %w", err)
			}
		}
		if processedAtStr != "" {
			if msg.ProcessedAt, err = time.Parse(time.RFC3339, processedAtStr); err != nil {
				return nil, fmt.Errorf("parse processed_at: %w", err)
			}
		}
		result = append(result, &msg)
	}
	return result, rows.Err()
}
