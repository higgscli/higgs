package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/state"
	"github.com/higgscli/higgs/internal/termio"
)

func newStateCmd() *cobra.Command {
	stateCmd := &cobra.Command{
		Use:   "state",
		Short: "View or manage the SQLite state database",
	}

	statsCmd := &cobra.Command{
		Use:   "stats [mailbox]",
		Short: "Show processing stats as JSON",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,7",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mailbox := ""
			if len(args) > 0 {
				mailbox = args[0]
			}
			return cmdStateStats(mailbox)
		},
	}

	clearCmd := &cobra.Command{
		Use:   "clear <mailbox>",
		Short: "Clear state for a mailbox",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,7",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdStateClear(args[0])
		},
	}

	queryCmd := &cobra.Command{
		Use:   "query [mailbox]",
		Short: "Query classification results as NDJSON",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,7",
		},
	}

	isMailingList := queryCmd.Flags().String("is-mailing-list", "", "Filter by mailing-list flag (\"true\" or \"false\", empty = no filter)")
	applied := queryCmd.Flags().String("applied", "", "Filter by applied flag (\"true\" or \"false\", empty = no filter)")
	minConfidence := queryCmd.Flags().Float64("min-confidence", 0, "Minimum confidence, inclusive (0-1)")
	maxConfidence := queryCmd.Flags().Float64("max-confidence", 0, "Maximum confidence, inclusive (0-1)")
	label := queryCmd.Flags().String("label", "", "Filter by suggested label (exact element match)")
	failed := queryCmd.Flags().Bool("failed", false, "Only messages whose label application failed")
	limit := queryCmd.Flags().Int("limit", 0, "Max rows to output (0 = all)")

	queryCmd.RunE = func(cmd *cobra.Command, args []string) error {
		filter := state.QueryFilter{
			Label:  *label,
			Failed: *failed,
			Limit:  *limit,
		}
		if len(args) > 0 {
			filter.Mailbox = args[0]
		}
		var err error
		if filter.IsMailingList, err = parseTriStateFlag("--is-mailing-list", *isMailingList); err != nil {
			return err
		}
		if filter.Applied, err = parseTriStateFlag("--applied", *applied); err != nil {
			return err
		}
		if cmd.Flags().Changed("min-confidence") {
			if *minConfidence < 0 || *minConfidence > 1 {
				return cerr.Validation("--min-confidence must be between 0 and 1, got %v", *minConfidence)
			}
			filter.MinConfidence = minConfidence
		}
		if cmd.Flags().Changed("max-confidence") {
			if *maxConfidence < 0 || *maxConfidence > 1 {
				return cerr.Validation("--max-confidence must be between 0 and 1, got %v", *maxConfidence)
			}
			filter.MaxConfidence = maxConfidence
		}
		return cmdStateQuery(filter)
	}

	stateCmd.AddCommand(statsCmd, clearCmd, queryCmd)
	return stateCmd
}

// parseTriStateFlag interprets a "true"/"false" string flag where the empty
// string means "no filter".
func parseTriStateFlag(name, value string) (*bool, error) {
	switch value {
	case "":
		return nil, nil
	case "true":
		b := true
		return &b, nil
	case "false":
		b := false
		return &b, nil
	default:
		return nil, cerr.Validation("%s must be \"true\" or \"false\", got %q", name, value)
	}
}

func cmdStateStats(mailbox string) error {
	tio := termio.Default()
	dbPath := os.Getenv("PM_STATE_DB")
	stateDB, err := state.Open(dbPath)
	if err != nil {
		return cerr.State(err, "open state DB")
	}
	defer stateDB.Close()

	effectiveDB := dbPath
	if effectiveDB == "" {
		effectiveDB = "~/.higgs/state.db"
	}

	if mailbox == "" {
		mailboxes, err := stateDB.ListMailboxes()
		if err != nil {
			return cerr.State(err, "list mailboxes")
		}

		type mailboxStats struct {
			Name    string `json:"name"`
			Total   int    `json:"total"`
			Applied int    `json:"applied"`
			Failed  int    `json:"failed"`
		}
		var mbStats []mailboxStats
		for _, mb := range mailboxes {
			total, applied, failed, err := stateDB.GetStats(mb)
			if err != nil {
				return cerr.State(err, "stats for %q", mb)
			}
			mbStats = append(mbStats, mailboxStats{Name: mb, Total: total, Applied: applied, Failed: failed})
		}
		if mbStats == nil {
			mbStats = []mailboxStats{}
		}

		total, applied, failed, err := stateDB.GetStats("")
		if err != nil {
			return cerr.State(err, "overall stats")
		}

		return tio.PrintJSON(map[string]any{
			"db":        effectiveDB,
			"mailboxes": mbStats,
			"total": map[string]any{
				"total":   total,
				"applied": applied,
				"failed":  failed,
			},
		})
	}

	total, applied, failed, err := stateDB.GetStats(mailbox)
	if err != nil {
		return cerr.State(err, "stats for %q", mailbox)
	}
	return tio.PrintJSON(map[string]any{
		"db":      effectiveDB,
		"mailbox": mailbox,
		"total":   total,
		"applied": applied,
		"failed":  failed,
	})
}

func cmdStateQuery(filter state.QueryFilter) error {
	tio := termio.Default()
	dbPath := os.Getenv("PM_STATE_DB")
	stateDB, err := state.Open(dbPath)
	if err != nil {
		return cerr.State(err, "open state DB")
	}
	defer stateDB.Close()

	msgs, err := stateDB.Query(filter)
	if err != nil {
		return cerr.State(err, "query state DB")
	}

	for _, msg := range msgs {
		if err := tio.PrintNDJSON(map[string]any{
			"type":             "message",
			"uid":              msg.UID,
			"mailbox":          msg.Mailbox,
			"uid_validity":     msg.UIDValidity,
			"subject":          msg.Subject,
			"from":             msg.From,
			"date":             msg.Date,
			"suggested_labels": msg.SuggestedLabels,
			"confidence":       msg.Confidence,
			"rationale":        msg.Rationale,
			"is_mailing_list":  msg.IsMailingList,
			"labels_applied":   msg.LabelsApplied,
			"apply_error":      msg.ApplyError,
			"processed_at":     msg.ProcessedAt,
		}); err != nil {
			return cerr.Internal(err, "write NDJSON")
		}
	}

	return tio.PrintNDJSON(map[string]any{
		"type":  "summary",
		"count": len(msgs),
	})
}

func cmdStateClear(mailbox string) error {
	tio := termio.Default()
	dbPath := os.Getenv("PM_STATE_DB")
	stateDB, err := state.Open(dbPath)
	if err != nil {
		return cerr.State(err, "open state DB")
	}
	defer stateDB.Close()

	if err := stateDB.ClearMailbox(mailbox, 0); err != nil {
		return cerr.State(err, "clear mailbox %q", mailbox)
	}

	return tio.PrintJSON(map[string]any{
		"cleared": true,
		"mailbox": mailbox,
	})
}
