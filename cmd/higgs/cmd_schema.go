package main

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/termio"
)

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema [subcommand]",
		Short: "Print machine-readable command metadata as JSON",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,3",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			if len(args) == 1 {
				target := findCommandByName(root, args[0])
				if target == nil {
					return cerr.Validation("unknown subcommand %q", args[0])
				}
				return termio.Default().PrintJSON(describeCommand(target))
			}
			return termio.Default().PrintJSON(describeRoot(root))
		},
	}
	return cmd
}

type schemaFlag struct {
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

type schemaArg struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
}

type schemaCommand struct {
	Name      string          `json:"name"`
	Short     string          `json:"short"`
	Long      string          `json:"long,omitempty"`
	Flags     []schemaFlag    `json:"flags"`
	Args      []schemaArg     `json:"args"`
	Stdout    string          `json:"stdout,omitempty"`
	ExitCodes []int           `json:"exit_codes"`
	Commands  []schemaCommand `json:"commands,omitempty"`
}

type schemaExitCodeDoc struct {
	Code        int    `json:"code"`
	Description string `json:"description"`
}

type schemaRoot struct {
	Commands     []schemaCommand     `json:"commands"`
	ExitCodeDocs []schemaExitCodeDoc `json:"exit_code_docs"`
}

func describeRoot(root *cobra.Command) schemaRoot {
	var cmds []schemaCommand
	for _, sub := range root.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		cmds = append(cmds, describeCommand(sub))
	}
	docs := make([]schemaExitCodeDoc, 0, len(cerr.ExitCodeDocs))
	for _, d := range cerr.ExitCodeDocs {
		docs = append(docs, schemaExitCodeDoc{Code: d.Code, Description: d.Description})
	}
	return schemaRoot{Commands: cmds, ExitCodeDocs: docs}
}

func describeCommand(c *cobra.Command) schemaCommand {
	out := schemaCommand{
		Name:      c.Name(),
		Short:     c.Short,
		Long:      c.Long,
		Flags:     collectFlags(c),
		Args:      deriveArgs(c),
		Stdout:    c.Annotations["stdout_format"],
		ExitCodes: parseExitCodes(c.Annotations["exit_codes"]),
	}
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		out.Commands = append(out.Commands, describeCommand(sub))
	}
	return out
}

func collectFlags(c *cobra.Command) []schemaFlag {
	flags := []schemaFlag{}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		flags = append(flags, schemaFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Type:        f.Value.Type(),
			Default:     f.DefValue,
			Description: f.Usage,
		})
	})
	return flags
}

func deriveArgs(c *cobra.Command) []schemaArg {
	use := strings.TrimSpace(c.Use)
	fields := strings.Fields(use)
	if len(fields) <= 1 {
		return []schemaArg{}
	}
	args := []schemaArg{}
	for _, raw := range fields[1:] {
		token := raw
		required := false
		switch {
		case strings.HasPrefix(token, "<") && strings.HasSuffix(token, ">"):
			required = true
			token = strings.TrimPrefix(strings.TrimSuffix(token, ">"), "<")
		case strings.HasPrefix(token, "[") && strings.HasSuffix(token, "]"):
			required = false
			token = strings.TrimPrefix(strings.TrimSuffix(token, "]"), "[")
		default:
			required = true
		}
		args = append(args, schemaArg{
			Name:     token,
			Required: required,
			Default:  commandArgDefault(c.Name(), token),
		})
	}
	return args
}

func commandArgDefault(cmdName, argName string) string {
	switch cmdName {
	case "classify", "apply-labels":
		if argName == "mailbox" {
			return "Folders/Accounts"
		}
	case "fetch-and-parse":
		if argName == "mailbox" {
			return "INBOX"
		}
	}
	return ""
}

func parseExitCodes(raw string) []int {
	if raw == "" {
		return []int{cerr.ExitCodeOK, cerr.ExitCodeInternal}
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func findCommandByName(root *cobra.Command, name string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
