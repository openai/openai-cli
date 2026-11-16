package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	localcommands "github.com/openai/openai-cli/internal/commands"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// Instructions and destinations verified against official documentation on 2026-10-08:
// https://learn.chatgpt.com/docs/cli
// https://learn.chatgpt.com/docs/config-file/config-basic
// https://learn.chatgpt.com/docs/app
// https://learn.chatgpt.com/docs/web
const (
	codexDocsURL   = "https://learn.chatgpt.com/docs/cli"
	codexConfigURL = "https://learn.chatgpt.com/docs/config-file/config-basic"
	codexAppURL    = "https://learn.chatgpt.com/docs/app"
	codexWebURL    = "https://chatgpt.com/"
	codexConfig    = "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\""
	codexOpenWait  = 10 * time.Second
)

type codexDestination struct {
	Destination string `json:"destination"`
	URL         string `json:"url"`
}

type codexInstallation struct {
	Method  string `json:"method"`
	Command string `json:"command"`
}

type codexInstructions struct {
	Command       string              `json:"command"`
	Installation  []codexInstallation `json:"installation"`
	DefaultConfig string              `json:"default_config"`
	ProjectConfig string              `json:"project_config"`
	ConfigExample string              `json:"config_example"`
	Notes         []string            `json:"notes"`
	Destinations  []codexDestination  `json:"destinations"`
}

// Register adds the local Codex guide to the command tree.
func Register(root *cli.Command) {
	root.Commands = append(root.Commands, codexCommand(terminal.OpenBrowser))
}

func codexCommand(open func(context.Context, string) error) *cli.Command {
	help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} codex --destination config

` + cli.CommandHelpTemplate + localcommands.LocalUtilityGlobalHelp
	return &cli.Command{
		Name: "codex", Usage: "Show local Codex CLI instructions and official links",
		Description: "Prints installation and configuration instructions for the separate codex command. " +
			"No API key, network access, or installed Codex CLI is needed. " +
			"Nothing is installed or configured. Use --destination to print one link; add --open to open it. " +
			"Supports --format auto, text, and json. Does not support --transform or --raw-output.",
		HideHelpCommand:    true,
		CustomHelpTemplate: help,
		Metadata:           map[string]any{localcommands.LocalUtilityMetadata: true, "local-help-full": help, "help-command-section": "Local tools"},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "destination", Usage: "Print an official link: docs, config, app, or web", OnlyOnce: true},
			&cli.BoolFlag{Name: "open", Usage: "Open the selected destination in a browser; requires --destination", OnlyOnce: true},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			return handleCodex(ctx, command, open)
		},
	}
}

func codexGuide() codexInstructions {
	return codexInstructions{
		Command: "codex",
		Installation: []codexInstallation{
			{Method: "npm", Command: "npm install -g @openai/codex"},
			{Method: "Homebrew", Command: "brew install --cask codex"},
		},
		DefaultConfig: "~/.codex/config.toml", ProjectConfig: ".codex/config.toml", ConfigExample: codexConfig,
		Notes: []string{
			"Codex CLI is a separate command named codex.",
			"Choose an installation method whose package manager is already installed.",
			"Start codex from your project directory. Codex CLI handles its own sign-in.",
			"CODEX_HOME can change the default configuration directory.",
			"Codex loads project configuration only for trusted projects.",
			"Web destinations retain their own sign-in and access requirements.",
		},
		Destinations: []codexDestination{
			{Destination: "docs", URL: codexDocsURL}, {Destination: "config", URL: codexConfigURL},
			{Destination: "app", URL: codexAppURL}, {Destination: "web", URL: codexWebURL},
		},
	}
}

func handleCodex(ctx context.Context, command *cli.Command, open func(context.Context, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if command.Args().Present() {
		return &localcommands.LocalError{Message: "Codex instructions take no positional arguments. Use --destination docs, config, app, or web."}
	}
	root := command.Root()
	if root.IsSet("transform") || root.IsSet("raw-output") {
		return &localcommands.LocalError{Message: "Codex instructions do not support --transform or --raw-output. Use --format text or json."}
	}
	format := strings.ToLower(root.String("format"))
	if format != "auto" && format != "text" && format != "json" {
		return &localcommands.LocalError{Message: "Codex instructions support --format auto, text, or json."}
	}
	var selected *codexDestination
	guide := codexGuide()
	if command.IsSet("destination") {
		for _, destination := range guide.Destinations {
			if command.String("destination") == destination.Destination {
				selected = &destination
				break
			}
		}
		if selected == nil {
			return &localcommands.LocalError{Message: "Choose a Codex destination: docs, config, app, or web."}
		}
	}
	if command.Bool("open") && selected == nil {
		return &localcommands.LocalError{Message: "Opening a Codex destination requires --destination docs, config, app, or web."}
	}
	if selected == nil {
		if handled, err := writeCodexTerminalGuide(ctx, command, guide); handled {
			if err != nil {
				return &localcommands.LocalError{Message: "Could not write Codex output. Output may be incomplete. Check the output file or pipe before rerunning.", Cause: err}
			}
			return nil
		}
	}
	var content string
	if format == "json" {
		var value any = guide
		if selected != nil {
			value = selected
		}
		payload, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		content = string(payload)
	} else if selected != nil {
		content = selected.URL
	} else {
		content = codexGuideText(guide)
	}
	if err := readable.WriteText(clioutput.Writer{Context: ctx, Sink: root.Writer}, content); err != nil {
		return &localcommands.LocalError{Message: "Could not write Codex output. Output may be incomplete. Check the output file or pipe before rerunning.", Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if command.Bool("open") {
		interruptContext, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		openContext, cancel := context.WithTimeout(interruptContext, codexOpenWait)
		defer cancel()
		err := open(openContext, selected.URL)
		if openContext.Err() != nil {
			err = openContext.Err()
		}
		if err != nil {
			return &localcommands.LocalError{Message: "Could not open the browser. Open this URL manually: " + selected.URL, Cause: err}
		}
	}
	return nil
}

func codexGuideText(guide codexInstructions) string {
	var text strings.Builder
	text.WriteString("Codex CLI is a separate command named codex.\n\n")
	for _, installation := range guide.Installation {
		fmt.Fprintf(&text, "Install with %s (choose one installed package manager):\n  %s\n", installation.Method, installation.Command)
	}
	text.WriteString("\nStart from your project directory:\n  codex\nCodex CLI handles its own sign-in.\n")
	fmt.Fprintf(&text, "\nDefault user configuration: %s\n", guide.DefaultConfig)
	text.WriteString("CODEX_HOME can change the default configuration directory.\n")
	fmt.Fprintf(&text, "Trusted project configuration: %s\n", guide.ProjectConfig)
	text.WriteString("Example configuration:\n  " + strings.ReplaceAll(guide.ConfigExample, "\n", "\n  ") + "\n\n")
	for _, destination := range guide.Destinations {
		fmt.Fprintf(&text, "%s: %s\n", destination.Destination, destination.URL)
	}
	text.WriteString("\nWeb destinations retain their own sign-in and access requirements.\n")
	return text.String()
}
