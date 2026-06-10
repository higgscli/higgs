package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/akeemjenkins/protoncli/internal/cerr"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "protoncli",
		Short:   "Local-only Proton Mail inbox management via IMAP + Ollama classification",
		Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		Long: `protoncli connects to Proton Mail Bridge over IMAP and uses a local Ollama
model to classify, label, and organize your inbox. All output is structured
JSON on stdout; progress and diagnostics go to stderr.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newApplyLabelsCmd(),
		newArchiveCmd(),
		newAskCmd(),
		newAttachmentsCmd(),
		newAuthCmd(),
		newBackfillCmd(),
		newClassifyCmd(),
		newCleanupLabelsCmd(),
		newDigestCmd(),
		newDraftCmd(),
		newExportCmd(),
		newExtractCmd(),
		newFetchAndParseCmd(),
		newFlagCmd(),
		newImportCmd(),
		newMarkReadCmd(),
		newMoveCmd(),
		newScanFoldersCmd(),
		newSchemaCmd(),
		newSearchCmd(),
		newSendCmd(),
		newStateCmd(),
		newSummarizeCmd(),
		newThreadCmd(),
		newThreadsCmd(),
		newTrashCmd(),
		newUnsubscribeCmd(),
		newWatchCmd(),
	)
	return root
}

func classifyCobraError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*cerr.Error); ok {
		return err
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "unknown flag"),
		strings.HasPrefix(msg, "unknown command"),
		strings.HasPrefix(msg, "unknown shorthand flag"),
		strings.HasPrefix(msg, "invalid argument"),
		strings.HasPrefix(msg, "flag needs an argument"),
		strings.HasPrefix(msg, "accepts"),
		strings.HasPrefix(msg, "requires"),
		strings.HasPrefix(msg, "bad flag syntax"):
		return cerr.Validation("%s", msg)
	}
	return err
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		cerr.Exit(classifyCobraError(err))
	}
}
