package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/state"
	"github.com/higgscli/higgs/internal/termio"
)

func newBackfillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backfill <logfile>",
		Short: "Parse a classify log file and populate the state DB",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,7,9",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdBackfill(args[0])
		},
	}
	return cmd
}

func cmdBackfill(logFile string) error {
	tio := termio.Default()

	f, err := os.Open(logFile)
	if err != nil {
		return cerr.Internal(err, "open log file")
	}
	defer f.Close()

	dbPath := os.Getenv("PM_STATE_DB")
	stateDB, err := state.Open(dbPath)
	if err != nil {
		return cerr.State(err, "open state DB")
	}
	defer stateDB.Close()

	appliedUIDs := make(map[uint32]bool)

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Applied labels to uid=") {
			idx := strings.Index(line, "uid=")
			if idx != -1 {
				rest := line[idx+4:]
				var uid uint32
				if _, err := fmt.Sscanf(rest, "%d", &uid); err == nil {
					appliedUIDs[uid] = true
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return cerr.Internal(err, "scan log (pass 1)")
	}

	if _, err := f.Seek(0, 0); err != nil {
		return cerr.Internal(err, "seek log file")
	}

	scanner = bufio.NewScanner(f)
	scanner.Buffer(buf, 10*1024*1024)

	var inserted, skippedCount, errorsCount int
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "{") {
			continue
		}

		var record struct {
			Mailbox         string   `json:"mailbox"`
			UID             uint32   `json:"uid"`
			UIDValidity     uint32   `json:"uid_validity"`
			Subject         string   `json:"subject"`
			From            string   `json:"from"`
			Date            string   `json:"date"`
			SuggestedLabels []string `json:"suggested_labels"`
			Confidence      float64  `json:"confidence"`
			Rationale       string   `json:"rationale"`
			IsMailingList   bool     `json:"is_mailing_list"`
			ApplyError      string   `json:"apply_error"`
			Error           string   `json:"error"`
		}

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			errorsCount++
			continue
		}

		if record.Error != "" {
			skippedCount++
			continue
		}

		if record.Mailbox == "" || record.UID == 0 || record.UIDValidity == 0 {
			skippedCount++
			continue
		}

		msgDate, _ := time.Parse(time.RFC3339, record.Date)

		labelsApplied := appliedUIDs[record.UID] && record.ApplyError == ""

		msg := &state.ProcessedMessage{
			Mailbox:         record.Mailbox,
			UIDValidity:     record.UIDValidity,
			UID:             record.UID,
			Subject:         record.Subject,
			From:            record.From,
			Date:            msgDate,
			SuggestedLabels: record.SuggestedLabels,
			Confidence:      record.Confidence,
			Rationale:       record.Rationale,
			IsMailingList:   record.IsMailingList,
			LabelsApplied:   labelsApplied,
			ApplyError:      record.ApplyError,
			ProcessedAt:     time.Now(),
		}

		if err := stateDB.MarkProcessed(msg); err != nil {
			termio.Warn("Failed to insert uid=%d: %v", record.UID, err)
			errorsCount++
			continue
		}
		inserted++
	}

	if err := scanner.Err(); err != nil {
		return cerr.Internal(err, "scan log (pass 2)")
	}

	total, applied, failed, _ := stateDB.GetStats("")

	return tio.PrintJSON(map[string]any{
		"inserted": inserted,
		"skipped":  skippedCount,
		"errors":   errorsCount,
		"totals": map[string]any{
			"total":   total,
			"applied": applied,
			"failed":  failed,
		},
	})
}
