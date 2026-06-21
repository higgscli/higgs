package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/classify"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/imapapply"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/parse"
	"github.com/higgscli/higgs/internal/state"
	"github.com/higgscli/higgs/internal/termio"
)

const BodySnippetCharsForClassify = 3000

func newClassifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "classify [mailbox]",
		Short: "Classify messages with Ollama and emit NDJSON results",
		Long: `Fetch messages in batches, classify each with Ollama (suggested labels +
mailing-list detection), emit one NDJSON line per message to stdout. Uses
SQLite to track processed messages for idempotency.`,
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,3,4,5,6,7",
		},
	}

	dryRun := cmd.Flags().Bool("dry-run", false, "Limit to 20 messages; show what the AI would do (no labels applied)")
	apply := cmd.Flags().Bool("apply", false, "Create missing labels and apply suggested labels to each message")
	limitFlag := cmd.Flags().Int("limit", 0, "Cap results (most recent N UIDs; 0 = use PM_CLASSIFY_LIMIT env)")
	noState := cmd.Flags().Bool("no-state", false, "Skip SQLite state tracking (reprocess all messages)")
	reprocess := cmd.Flags().Bool("reprocess", false, "Reprocess messages even if already in state DB")
	workersFlag := cmd.Flags().Int("workers", 0, "Number of parallel classification workers (0 = use PM_CLASSIFY_WORKERS env, default 4)")
	minConfidence := cmd.Flags().Float64("min-confidence", 0.0, "Skip label application when confidence is below this threshold (0.0-1.0)")
	oldestFirst := cmd.Flags().Bool("oldest-first", false, "Select the oldest N UIDs instead of the most recent (for backfill workflows)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		mailbox := "Folders/Accounts"
		if len(args) > 0 {
			mailbox = args[0]
		}
		if *apply && *dryRun {
			return cerr.Validation("cannot use --apply with --dry-run")
		}
		if *minConfidence < 0 || *minConfidence > 1 {
			return cerr.Validation("--min-confidence must be between 0.0 and 1.0")
		}
		return cmdClassify(mailbox, *dryRun, *apply, *limitFlag, *noState, *reprocess, *workersFlag, *minConfidence, *oldestFirst)
	}

	return cmd
}

type classifyJob struct {
	msg     email.Message
	fetched imapfetch.FetchedMessage
}

type classifyResult struct {
	msg    email.Message
	result *classify.Result
	err    error
}

func cmdClassify(mailbox string, dryRun, apply bool, limitFlag int, noState, reprocess bool, workersFlag int, minConfidence float64, oldestFirst bool) error {
	tio := termio.Default()

	termio.Info("Loading config from environment")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return cerr.Config("%s", err.Error())
	}

	var stateDB *state.DB
	if !noState {
		dbPath := os.Getenv("PM_STATE_DB")
		stateDB, err = state.Open(dbPath)
		if err != nil {
			return cerr.State(err, "open state DB")
		}
		defer stateDB.Close()
		if dbPath == "" {
			termio.Info("State tracking enabled (default: ~/.higgs/state.db)")
		} else {
			termio.Info("State tracking enabled (%s)", dbPath)
		}
	} else {
		termio.Info("State tracking disabled (--no-state)")
	}

	limit := getEnvInt("PM_CLASSIFY_LIMIT", 100)
	limitSource := "PM_CLASSIFY_LIMIT"
	if dryRun {
		limit = 20
		limitSource = "dry-run"
		termio.Info("Dry run: limiting to 20 messages (review only; no labels applied)")
	} else if limitFlag > 0 {
		limit = limitFlag
		limitSource = "--limit"
	}
	termio.Info("Effective limit: %d (from %s)", limit, limitSource)

	numWorkers := getEnvInt("PM_CLASSIFY_WORKERS", 4)
	if workersFlag > 0 {
		numWorkers = workersFlag
	}
	if numWorkers < 1 {
		numWorkers = 1
	}
	termio.Info("Parallel workers: %d", numWorkers)

	batchSize := getEnvInt("PM_CLASSIFY_BATCH_SIZE", 50)
	if batchSize <= 0 {
		batchSize = 50
	}

	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("failed to connect/login IMAP: %s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	var existingMailboxes map[string]bool
	applyTimeout := time.Duration(getEnvInt("PM_IMAP_APPLY_TIMEOUT", 180)) * time.Second
	if applyTimeout <= 0 {
		applyTimeout = 180 * time.Second
	}
	requestedMB := mailbox
	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST mailboxes")
	}
	mailbox, err = imaputil.ResolveMailboxName(mailbox, mboxes)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if mailbox != requestedMB {
		termio.Info("Resolved mailbox %q to server name %q", requestedMB, mailbox)
	}
	if apply {
		existingMailboxes = imapapply.BuildMailboxSet(mboxes)
		c.Timeout = applyTimeout
		termio.Info("Apply mode ON: will create missing Labels/... and apply suggested labels to each message (IMAP command timeout: %v)", applyTimeout)
		termio.Info("Existing mailboxes: %d (labels already present will be reused)", len(existingMailboxes))
	}

	termio.Info("Selecting mailbox %q (read-only)", mailbox)
	snap, err := imapfetch.SelectMailbox(c, mailbox)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", mailbox)
	}
	termio.Info("Mailbox UIDVALIDITY=%d", snap.UIDValidity)

	if stateDB != nil {
		if err := stateDB.ClearMailbox(mailbox, snap.UIDValidity); err != nil {
			termio.Warn("Failed to clear old state: %v", err)
		}
	}

	uids, err := imapfetch.SearchUIDs(c, time.Time{}, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "UID SEARCH")
	}
	termio.Info("Found %d UIDs in mailbox", len(uids))
	if len(uids) == 0 {
		return tio.PrintNDJSON(map[string]any{
			"type":       "summary",
			"mailbox":    mailbox,
			"classified": 0,
			"errors":     0,
			"skipped":    0,
		})
	}

	var skippedCount int
	if stateDB != nil && !reprocess {
		processedUIDs, err := stateDB.GetProcessedUIDs(mailbox, snap.UIDValidity)
		if err != nil {
			return cerr.State(err, "get processed UIDs")
		}
		if len(processedUIDs) > 0 {
			var filtered []uint32
			for _, uid := range uids {
				if !processedUIDs[uid] {
					filtered = append(filtered, uid)
				} else {
					skippedCount++
				}
			}
			uids = filtered
			termio.Info("Skipping %d already-processed messages (use --reprocess to override)", skippedCount)
		}
	}

	if len(uids) == 0 {
		termio.Info("No new messages to classify")
		return tio.PrintNDJSON(map[string]any{
			"type":       "summary",
			"mailbox":    mailbox,
			"classified": 0,
			"skipped":    skippedCount,
			"errors":     0,
		})
	}

	if limit > 0 && len(uids) > limit {
		if oldestFirst {
			uids = uids[:limit]
			termio.Info("Limiting to %d oldest UIDs (from %s)", limit, limitSource)
		} else {
			uids = uids[len(uids)-limit:]
			termio.Info("Limiting to %d most recent UIDs (from %s)", limit, limitSource)
		}
	}

	ctx := context.Background()
	totalMsgs := len(uids)
	var processed int64
	var classifyErrors int64

	termio.Info("Processing %d messages from %q (parallel: %d workers)", totalMsgs, mailbox, numWorkers)
	if skippedCount > 0 {
		termio.Info("(Skipped %d already-processed)", skippedCount)
	}

	jobs := make(chan classifyJob, numWorkers*2)
	results := make(chan classifyResult, numWorkers*2)

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				result, err := classify.Classify(ctx, cfg.Ollama.BaseURL, cfg.Ollama.Model, &job.msg)
				results <- classifyResult{
					msg:    job.msg,
					result: result,
					err:    err,
				}
			}
		}(w)
	}

	var resultsMu sync.Mutex
	var allResults []classifyResult
	done := make(chan struct{})
	go func() {
		for r := range results {
			resultsMu.Lock()
			allResults = append(allResults, r)
			count := atomic.AddInt64(&processed, 1)
			if r.err != nil {
				atomic.AddInt64(&classifyErrors, 1)
				termio.Error("[%d/%d] classify uid=%d: %v", count, totalMsgs, r.msg.UID, r.err)
			} else {
				termio.Info("[%d/%d] classified uid=%d labels=%v", count, totalMsgs, r.msg.UID, r.result.SuggestedLabels)
			}
			resultsMu.Unlock()
		}
		close(done)
	}()

	for i := 0; i < len(uids); i += batchSize {
		end := i + batchSize
		if end > len(uids) {
			end = len(uids)
		}
		batch := uids[i:end]

		termio.Info("Fetching batch %d-%d of %d...", i+1, end, len(uids))
		msgs, fetchErr := imapfetch.FetchRFC822(c, batch)
		if fetchErr != nil {
			wrapped := imapclient.Wrap(fetchErr)
			if imapclient.IsConnectionError(wrapped) {
				termio.Warn("FETCH batch failed (connection error); reconnecting and retrying once: %v", fetchErr)
				oldC := c
				c, err = imapclient.Dial(cfg.IMAP)
				if err != nil {
					close(jobs)
					wg.Wait()
					close(results)
					return cerr.Auth("reconnect after FETCH error: %s", err.Error())
				}
				_ = oldC.Terminate()
				if apply {
					c.Timeout = applyTimeout
					mboxes, err := imaputil.ListMailboxes(c, false)
					if err != nil {
						close(jobs)
						wg.Wait()
						close(results)
						return cerr.IMAP(imapclient.Wrap(err), "LIST after reconnect")
					}
					existingMailboxes = imapapply.BuildMailboxSet(mboxes)
				}
				snap, err = imapfetch.SelectMailbox(c, mailbox)
				if err != nil {
					close(jobs)
					wg.Wait()
					close(results)
					return cerr.IMAP(imapclient.Wrap(err), "SELECT after reconnect")
				}
				msgs, fetchErr = imapfetch.FetchRFC822(c, batch)
			}
			if fetchErr != nil {
				close(jobs)
				wg.Wait()
				close(results)
				return cerr.IMAP(imapclient.Wrap(fetchErr), "FETCH batch")
			}
		}

		for _, m := range msgs {
			body, _ := parse.BestBodyText(bytes.NewReader(m.RFC822))
			snippet := parse.Snippet(body, BodySnippetCharsForClassify)
			msg := fetchedToMessage(&m, body, snippet, mailbox, snap.UIDValidity)
			jobs <- classifyJob{msg: msg, fetched: m}
		}
	}

	close(jobs)
	wg.Wait()
	close(results)
	<-done

	termio.Info("Classification complete. Saving results...")

	var appliedCount, applyFailCount, skippedByThresholdCount int
	for _, r := range allResults {
		row := map[string]any{
			"mailbox":      mailbox,
			"uid":          r.msg.UID,
			"uid_validity": snap.UIDValidity,
			"subject":      r.msg.Subject,
			"from":         r.msg.From,
			"date":         r.msg.Date,
		}

		if r.err != nil {
			row["error"] = cerr.From(cerr.Classify(r.err, "%s", r.err.Error())).ToEnvelope()["error"]
			if err := tio.PrintNDJSON(row); err != nil {
				return cerr.Internal(err, "write NDJSON")
			}
			continue
		}

		row["suggested_labels"] = r.result.SuggestedLabels
		row["confidence"] = r.result.Confidence
		row["rationale"] = r.result.Rationale
		row["is_mailing_list"] = r.result.IsMailingList

		if dryRun {
			row["dry_run"] = true
		}

		belowThreshold := minConfidence > 0 && r.result.Confidence < minConfidence
		if belowThreshold {
			row["skipped_by_threshold"] = true
			skippedByThresholdCount++
		}

		labelsApplied := false
		var applyError string

		if apply && !belowThreshold && len(r.result.SuggestedLabels) > 0 {
			if err := imapapply.ApplyLabels(c, mailbox, r.msg.UID, r.result.SuggestedLabels, existingMailboxes); err != nil {
				termio.Error("Apply failed uid=%d: %v", r.msg.UID, err)
				row["apply_error"] = cerr.From(cerr.IMAP(imapclient.Wrap(err), "%s", err.Error())).ToEnvelope()["error"]
				applyError = err.Error()
				applyFailCount++
			} else {
				labelsApplied = true
				appliedCount++
			}
		}

		if apply {
			row["labels_applied"] = labelsApplied
		}

		if err := tio.PrintNDJSON(row); err != nil {
			return cerr.Internal(err, "write NDJSON")
		}

		if stateDB != nil {
			msgDate, _ := time.Parse(time.RFC3339, fmt.Sprintf("%v", r.msg.Date))
			stateMsg := &state.ProcessedMessage{
				Mailbox:         mailbox,
				UIDValidity:     snap.UIDValidity,
				UID:             r.msg.UID,
				Subject:         r.msg.Subject,
				From:            r.msg.From,
				Date:            msgDate,
				SuggestedLabels: r.result.SuggestedLabels,
				Confidence:      r.result.Confidence,
				Rationale:       r.result.Rationale,
				IsMailingList:   r.result.IsMailingList,
				LabelsApplied:   labelsApplied,
				ApplyError:      applyError,
				ProcessedAt:     time.Now(),
			}
			if err := stateDB.MarkProcessed(stateMsg); err != nil {
				termio.Warn("Failed to record state for uid=%d: %v", r.msg.UID, err)
			}
		}
	}

	summary := map[string]any{
		"type":       "summary",
		"mailbox":    mailbox,
		"classified": processed,
		"errors":     classifyErrors,
		"skipped":    skippedCount,
	}
	if minConfidence > 0 {
		summary["skipped_by_threshold"] = skippedByThresholdCount
		summary["min_confidence"] = minConfidence
	}
	if apply {
		summary["applied"] = appliedCount
		summary["apply_failed"] = applyFailCount
	}
	if stateDB != nil {
		total, applied, failed, _ := stateDB.GetStats(mailbox)
		summary["state_db"] = map[string]any{
			"total":   total,
			"applied": applied,
			"failed":  failed,
		}
	}

	termio.Info("Done. Classified %d messages (%d errors)", processed, classifyErrors)
	return tio.PrintNDJSON(summary)
}
