package explore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/urfave/cli/v3"
)

// commandExploreError retains fixed diagnostics and the original failure identity.
// Input streams and terminal errors can contain paths, so never expose their text.
type commandExploreError struct {
	message string
	code    int
	cause   error
}

func (e *commandExploreError) Error() string { return e.message }
func (e *commandExploreError) ExitCode() int { return e.code }
func (e *commandExploreError) Unwrap() error { return e.cause }

func exploreFailure(message string, code int, cause error) error {
	return &commandExploreError{message: message, code: code, cause: &localcommands.LocalError{Message: message, Cause: cause}}
}

// Register adds local command search without remote setup.
func Register(root *cli.Command) {
	if root.Command("explore") != nil {
		return
	}
	root.Commands = append(root.Commands, &cli.Command{
		Name:        "explore",
		Metadata:    map[string]any{localcommands.LocalUtilityMetadata: true},
		Usage:       "Search local CLI commands and examples without running them.",
		Description: "Type to search. Arrow keys choose a command. Enter selects it; Escape cancels.\nWith command insertion enabled, zsh and fish fill your editable prompt. Bash stages the selection; Ctrl+X, then Tab inserts it. Without integration, selection prints the command. Search needs no API key or internet.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "suggestions", Hidden: true},
			&cli.BoolFlag{Name: "shell-integration", Hidden: true},
			&cli.StringFlag{Name: "shell", Hidden: true},
		},
		Action: commandExplore,
	})
}

func exploreCandidates(root *cli.Command, invocation string) []commandsuggest.Candidate {
	return commandsuggest.Catalog(root, invocation, clihelp.VisibleCommands, func(command *cli.Command, path string) []commandsuggest.Candidate {
		var candidates []commandsuggest.Candidate
		for _, example := range clihelp.CommandExamples(command, path) {
			candidates = append(candidates, commandsuggest.Candidate{Command: invocation + " " + example.Command, Description: example.Description})
		}
		return candidates
	})
}

func commandExplore(ctx context.Context, command *cli.Command) error {
	root := command.Root()
	format := strings.ToLower(root.String("format"))
	if format != "" && format != "auto" && format != "text" || root.String("transform") != "" || root.Bool("raw-output") {
		return exploreFailure("Command search supports auto or text output formats. Transformations and raw output are unavailable.", 2, nil)
	}
	errorFormat := strings.ToLower(root.String("format-error"))
	if (errorFormat != "" && errorFormat != "auto" && errorFormat != "text") || root.String("transform-error") != "" {
		return exploreFailure("Command search requires auto or text error output without transformations.", 2, nil)
	}
	integration := command.Bool("shell-integration")
	if command.Args().Len() != 0 || command.Bool("suggestions") && !integration {
		return exploreFailure("Run openai explore without arguments. Recent suggestions require shell integration.", 2, nil)
	}
	switch command.String("shell") {
	case "", "bash", "zsh", "fish":
	default:
		return exploreFailure("Command insertion supports Bash, zsh, or fish.", 2, nil)
	}
	if err := ctx.Err(); err != nil {
		return exploreFailure("Command search was interrupted.", 130, err)
	}
	if !term.IsTerminal(os.Stderr.Fd()) || os.Getenv("TERM") == "dumb" {
		return exploreFailure("Command search needs an interactive terminal with cursor support.", 2, nil)
	}
	if (!integration && (!term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()))) ||
		(integration && !command.Bool("suggestions") && !term.IsTerminal(os.Stdin.Fd())) {
		return exploreFailure("Run openai explore in an interactive terminal without pipes or redirection.", 2, nil)
	}
	candidates := exploreCandidates(root, clihelp.Invocation(root.Name, os.Args))
	title := "Explore commands"
	if command.Bool("suggestions") {
		title = "Recent commands"
		reader := root.Reader
		if reader == nil {
			reader = os.Stdin
		}
		if file, ok := reader.(interface{ Fd() uintptr }); ok && term.IsTerminal(file.Fd()) {
			return exploreFailure("Recent command suggestions require a shell-provided input stream.", 2, nil)
		}
		var err error
		candidates, err = commandsuggest.ReadCandidates(reader)
		if err != nil {
			return exploreFailure("Recent command suggestions are unavailable or invalid. Run a CLI help command and retry.", 2, err)
		}
	}
	input, err := commandsuggest.OpenPickerInput(os.Stderr)
	if err != nil {
		return exploreFailure("Could not open the terminal for command search.", 2, err)
	}
	defer input.Close()
	fillsPrompt := integration && (command.String("shell") != "bash" || command.Bool("suggestions"))
	selected, err := commandsuggest.Pick(ctx, input, os.Stderr, candidates, title, fillsPrompt)
	if errors.Is(err, commandsuggest.ErrCanceled) || errors.Is(err, tea.ErrInterrupted) || errors.Is(err, context.Canceled) {
		return exploreFailure("Command selection canceled.", 130, err)
	}
	if err != nil {
		return exploreFailure("Command search could not finish. Check your terminal and retry.", 1, err)
	}
	output := root.Writer
	if output == nil {
		output = os.Stdout
	}
	if _, err := fmt.Fprintln(output, selected); err != nil {
		return exploreFailure("Could not write the selected command.", 1, err)
	}
	if !integration {
		_, err = io.WriteString(os.Stderr, "Selected command only. Enable command insertion to fill your shell prompt: openai @completion --help\n")
	}
	if err != nil {
		return exploreFailure("Could not display command insertion guidance.", 1, err)
	}
	return nil
}
