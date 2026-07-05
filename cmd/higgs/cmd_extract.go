package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/llm"
	"github.com/higgscli/higgs/internal/termio"
)

func newExtractCmd() *cobra.Command {
	var (
		schemaFile string
		preset     string
		uidsFlag   string
		model      string
	)
	cmd := &cobra.Command{
		Use:   "extract <mailbox>",
		Short: "Extract structured data from messages with a JSON schema",
		Long: `Run the model with a JSON schema (either --preset or --schema FILE) to
extract structured data from each selected UID.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5,6",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdExtract(args[0], schemaFile, preset, uidsFlag, model)
		},
	}
	cmd.Flags().StringVar(&schemaFile, "schema", "", "Path to a JSON schema file")
	cmd.Flags().StringVar(&preset, "preset", "", "Preset schema: invoice|shipping|meeting")
	cmd.Flags().StringVar(&uidsFlag, "uid", "", "Comma-separated UIDs to extract from")
	cmd.Flags().StringVar(&model, "model", "", "Override Ollama model (defaults to PM_OLLAMA_MODEL)")
	return cmd
}

func loadExtractSchema(schemaFile, preset string) (map[string]any, error) {
	schemaFile = strings.TrimSpace(schemaFile)
	preset = strings.TrimSpace(preset)
	if schemaFile == "" && preset == "" {
		return nil, cerr.Validation("one of --schema or --preset is required")
	}
	if schemaFile != "" && preset != "" {
		return nil, cerr.Validation("--schema and --preset are mutually exclusive")
	}
	if preset != "" {
		m, err := llm.Preset(preset)
		if err != nil {
			return nil, cerr.Validation("unknown preset %q", preset)
		}
		return m, nil
	}
	b, err := os.ReadFile(schemaFile)
	if err != nil {
		return nil, cerr.Validation("read schema: %s", err.Error())
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, cerr.Validation("parse schema JSON: %s", err.Error())
	}
	return m, nil
}

func cmdExtract(mailbox, schemaFile, preset, uidsFlag, model string) error {
	schema, err := loadExtractSchema(schemaFile, preset)
	if err != nil {
		return err
	}
	uids, err := parseUIDList(uidsFlag)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if len(uids) == 0 {
		return cerr.Validation("--uid is required (comma-separated)")
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	if model == "" {
		model = cfg.Ollama.Model
	}

	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("%s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST failed")
	}
	resolved, err := imaputil.ResolveMailboxName(mailbox, mboxes)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if _, err := imapfetch.SelectMailbox(c, resolved); err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", resolved)
	}

	fetched, err := imapfetch.FetchRFC822(c, uids)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "FETCH")
	}

	tio := termio.Default()
	ctx := context.Background()
	failed, err := reportMissingUIDs(tio, resolved, uids, fetched)
	if err != nil {
		return err
	}
	succeeded := 0
	for _, f := range fetched {
		m := fetchedToLLMMessage(f, resolved)
		data, eErr := llm.Extract(ctx, cfg.Ollama.BaseURL, model, m, schema)
		if eErr != nil {
			failed++
			if perr := tio.PrintNDJSON(map[string]any{
				"type":    "extraction",
				"uid":     f.UID,
				"mailbox": resolved,
				"error":   cerr.From(eErr).ToEnvelope()["error"],
			}); perr != nil {
				return cerr.Internal(perr, "write NDJSON")
			}
			continue
		}
		if err := tio.PrintNDJSON(map[string]any{
			"type":    "extraction",
			"uid":     f.UID,
			"mailbox": resolved,
			"data":    data,
		}); err != nil {
			return cerr.Internal(err, "write NDJSON")
		}
		succeeded++
	}
	return tio.PrintNDJSON(map[string]any{
		"type":    "summary",
		"mailbox": resolved,
		"count":   succeeded,
		"failed":  failed,
	})
}

