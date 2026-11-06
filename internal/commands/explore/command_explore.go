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
	"github.com/openai/openai-cli/internal/terminal"
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
	command := &cli.Command{
		Name:        "explore",
		Metadata:    map[string]any{localcommands.LocalUtilityMetadata: true, "help-command-section": "Local tools"},
		Usage:       "Search local CLI commands and examples without running them.",
		ArgsUsage:   "[SEARCH_TERMS...]",
		Description: "Pass search terms to print matching commands and examples, ranked by relevance. Search works offline and in pipes.\n\nWithout search terms, browse commands alphabetically. Type to search. Arrow keys choose a command. Enter selects it. Escape cancels.\nWith shell integration, zsh and fish put the selection at your prompt. In Bash 4.3+, press Ctrl+X, then Tab to insert it. If you installed a standalone binary, press F2 when offered to set up insertion.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "suggestions", Hidden: true},
			&cli.BoolFlag{Name: "shell-integration", Hidden: true},
			&cli.StringFlag{Name: "shell", Hidden: true},
		},
		Action: commandExplore,
	}
	const output = "Search supports auto or text for --format and --format-error."
	command.Metadata["help-content"] = clihelp.Content{
		Description:       command.Description,
		GlobalOptionsNote: output,
		InheritedFlagHelp: map[string]string{
			"format": output, "format-error": output,
			"transform": "Search does not support --transform.", "raw-output": "Search does not support --raw-output.",
			"transform-error": "Search does not support --transform-error.",
		},
		Examples: []clihelp.Example{
			{Description: "Open interactive command search:", Command: "explore"},
			{Description: "Find a command by describing the task:", Command: `explore "count tokens offline"`},
		},
	}
	root.Commands = append(root.Commands, command)
}

// Add a phrase when the ordinary command name and description miss or reverse a user task.
var taskPhrases = map[string][]commandsuggest.TaskPhrase{
	"models list": {
		{Text: "show all available models", AnyWordOrder: true},
		{Text: "see all models", AnyWordOrder: true},
	},
	"images generate": {
		{Text: "image generation"},
		{Text: "make an image", AnyWordOrder: true},
		{Text: "create an image", AnyWordOrder: true},
	},
	"tokenizer count": {{Text: "count tokens offline", AnyWordOrder: true}},
	"speak":           {{Text: "text to speech"}, {Text: "text to audio"}, {Text: "read text aloud"}},
	"transcribe":      {{Text: "speech to text"}},
}

func exploreCandidates(root *cli.Command, invocation, shell string) []commandsuggest.Candidate {
	native := clihelp.NativeInvocation(root, invocation)
	return commandsuggest.Catalog(root, native, taskPhrases, clihelp.VisibleCommands, func(command *cli.Command, path string) []commandsuggest.Candidate {
		// Search examples describe queries, not additional destinations.
		if path == "explore" {
			return nil
		}
		if content, _ := command.Metadata["help-content"].(clihelp.Content); content.BashExamples && shell != "bash" && shell != "zsh" {
			return nil
		}
		run := clihelp.ExampleInvocation(command, invocation, native)
		var candidates []commandsuggest.Candidate
		for _, example := range clihelp.CommandExamples(command, path) {
			candidates = append(candidates, commandsuggest.Example(run, example.Command, example.Description))
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
	query := strings.Join(strings.Fields(strings.Join(command.Args().Slice(), " ")), " ")
	if command.Args().Len() > 0 && (query == "" || integration || command.Bool("suggestions") || command.IsSet("shell")) {
		return exploreFailure("Provide nonempty search terms without shell-integration options.", 2, nil)
	}
	if command.Bool("suggestions") && !integration {
		return exploreFailure("Recent suggestions require shell integration.", 2, nil)
	}
	switch command.String("shell") {
	case "", "bash", "zsh", "fish":
	default:
		return exploreFailure("Command insertion supports Bash, zsh, or fish.", 2, nil)
	}
	if err := ctx.Err(); err != nil {
		return exploreFailure("Command search was interrupted.", 130, err)
	}
	shell := command.String("shell")
	if shell == "" {
		shell = terminal.ParentShell(ctx)
	}
	if query != "" {
		matches := commandsuggest.Search(exploreCandidates(root, clihelp.Invocation(root.Name, os.Args), shell), query)
		if err := ctx.Err(); err != nil {
			return exploreFailure("Command search was interrupted.", 130, err)
		}
		output := root.Writer
		if output == nil {
			output = os.Stdout
		}
		text := clihelp.SearchResults(command, matches)
		n, err := io.WriteString(output, text)
		if err == nil && n != len(text) {
			err = io.ErrShortWrite
		}
		if err != nil {
			commandsuggest.Invalidate(command)
			return exploreFailure("Could not write command search results.", 1, err)
		}
		return nil
	}
	if !term.IsTerminal(os.Stderr.Fd()) || os.Getenv("TERM") == "dumb" {
		return exploreFailure("Command search needs an interactive terminal with cursor support.", 2, nil)
	}
	if (!integration && (!term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()))) ||
		(integration && !command.Bool("suggestions") && !term.IsTerminal(os.Stdin.Fd())) {
		return exploreFailure("Run openai explore in an interactive terminal without pipes or redirection.", 2, nil)
	}
	var candidates []commandsuggest.Candidate
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
	} else {
		candidates = exploreCandidates(root, clihelp.Invocation(root.Name, os.Args), shell)
	}
	input, err := terminal.OpenInputFromOutput(os.Stderr)
	if err != nil {
		return exploreFailure("Could not open the terminal for command search.", 2, err)
	}
	defer input.Close()
	fillsPrompt := integration && (command.String("shell") != "bash" || command.Bool("suggestions"))
	var setup exploreInsertionSetup
	if !integration {
		setup = insertionSetupForPicker(ctx, terminal.ParentShell(ctx))
	}
	selected, err := commandsuggest.Pick(ctx, input, os.Stderr, candidates, title, fillsPrompt, setup.prompt)
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
	if selected.EnableInsertion {
		return applyCommandInsertionSetup(ctx, output, format, setup.shell, "install", setup.targets)
	}
	if _, err := fmt.Fprintln(output, selected.Command); err != nil {
		return exploreFailure("Could not write the selected command.", 1, err)
	}
	if setup.prompt != "" {
		_, err = io.WriteString(os.Stderr, "To fill the prompt next time, open Explore and press F2 to set up insertion.\n")
	} else if !integration {
		_, err = fmt.Fprintln(os.Stderr, clihelp.CompletionSetupGuidance(ctx, root))
	}
	if err != nil {
		return exploreFailure("Could not display command insertion guidance.", 1, err)
	}
	return nil
}
