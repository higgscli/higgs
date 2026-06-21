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

	stateCmd.AddCommand(statsCmd, clearCmd)
	return stateCmd
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
