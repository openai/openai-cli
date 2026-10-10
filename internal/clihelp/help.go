// Package clihelp owns local onboarding and help-topic routing. Feature commands
// supply complete guidance through command metadata.
package clihelp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/urfave/cli/v3"
)

const setupHelp = `{{$run := index .Root.Metadata "help-invocation"}}Set up your API key

1. CREATE A KEY
   https://platform.openai.com/settings/organization/api-keys

2. ENTER IT IN YOUR SHELL
   Choose the instructions for the shell you use.

   Bash or zsh (macOS / Linux)
     Run this line, paste your key (hidden), then press Enter:
       read -rs OPENAI_API_KEY
     Then run this line so the CLI can read the key:
       export OPENAI_API_KEY
       printf '\n'

   PowerShell (Windows, macOS, or Linux)
     Run this line, paste your key (hidden), then press Enter:
       $openaiKey = Read-Host "API key" -AsSecureString
     Then run these lines:
       $env:OPENAI_API_KEY = [System.Net.NetworkCredential]::new("", $openaiKey).Password
       Remove-Variable openaiKey

   Using Command Prompt (cmd.exe)? Run powershell first, then use the
   PowerShell steps above. Run the CLI in that same PowerShell session.
   Using Fish? Run bash first, then use the Bash steps above.

   Windows command examples use PowerShell syntax. In cmd.exe, run
   openai from PATH or .\openai.exe from the folder containing the CLI.

   These steps set the key for this shell session. A new window needs it again.

3. TRY A COMMAND
   {{$run}} models list

This is a guide only. No key has been entered or checked by showing this page.
`

// Configure keeps onboarding in CLI help, never in a shell startup file or
// an API command's output. Normalize help before Run so the framework still
// owns parsing, parent links and rendering, while help skips request setup.
// Commands marked "local-help" only display guidance. Existing authored guides
// remain intact, with "local-help-full" preferred when supplied.
func Configure(root *cli.Command, args []string) ([]string, bool, error) {
	installSubcommandHelp()
	root.CustomRootCommandHelpTemplate = commandHelpTemplate
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}
	root.Metadata["help-invocation"] = Invocation(root.Name, args)
	root.Metadata["help-legacy-all"] = false
	delete(root.Metadata, "help-selected-command")
	if root.Command("help") == nil {
		root.Commands = append(root.Commands, &cli.Command{
			Name: "help", Usage: "Get help: help [command...]", HideHelpCommand: true,
			Metadata:           map[string]any{"help-command-section": "Help", "help-command-rank": 100000, "help-topic-command": true},
			CustomHelpTemplate: `{{call (index .Root.Metadata "complete-help")}}`,
			Flags:              []cli.Flag{&cli.BoolFlag{Name: "all", Usage: "Use help without --all", HideDefault: true, Hidden: true}},
			Action:             showHelpTopics,
			Commands: []*cli.Command{{
				Name: "setup", Usage: "Learn how to enter your API key", HideHelpCommand: true,
				CustomHelpTemplate: setupHelp,
				Action: func(_ context.Context, command *cli.Command) error {
					if command.Args().Present() {
						return cli.Exit("Setup help takes no additional arguments.", 3)
					}
					cli.HelpPrinter(command.Root().Writer, setupHelp, command)
					return nil
				},
			}},
		})
		requestSetup := root.Before
		root.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
			// Inspect parsed command selection, never arbitrary token values.
			if command.Args().First() == "help" || requestSetup == nil {
				return ctx, nil
			}
			return requestSetup(ctx, command)
		}
	}
	if help := root.Command("help"); help != nil && help.Metadata["help-topic-command"] == true {
		for _, declared := range help.Flags {
			if flag, ok := declared.(*cli.BoolFlag); ok && flag.Name == "all" {
				// urfave retains IsSet between runs. Reset only our migration
				// flag so another Configure call cannot retain an old notice.
				*flag = cli.BoolFlag{Name: "all", Usage: "Use help without --all", HideDefault: true, Hidden: true}
			}
		}
	}
	configureCommandHelp(root, root.Metadata["help-invocation"].(string), "")
	if len(args) <= 1 {
		return []string{root.Name, "--help"}, true, nil
	}
	if args[1] == "__complete" {
		return args, false, nil
	}
	if normalized, help := normalizeHelpFlag(root, args); help {
		return normalized, true, nil
	}
	current := root
	commandStart := -1
	for i := 1; i < len(args); i++ {
		arg := args[i]
		// On a leaf, "help" can be a filename or another positional operand.
		// Only resource groups accept the help-command shorthand.
		if arg == "help" && len(current.Commands) > 0 {
			// Use the parsed help action for both spellings. It resolves topics
			// after global flags are parsed, so failures honor error formatting.
			// Keep root-only flags before help. Move its token before the
			// resource path; inherited flags and their values retain order.
			helpAt := i
			if commandStart >= 0 {
				helpAt = commandStart
			}
			out := append(append([]string(nil), args[:helpAt]...), "help")
			out = append(out, args[helpAt:i]...)
			literal := false
			for _, topic := range args[i+1:] {
				literal = literal || topic == "--"
				if literal || topic != "--help" && topic != "-h" && topic != "--h" {
					out = append(out, topic)
				}
			}
			return out, true, nil
		}
		// Flags and values remain entirely under the framework parser. In
		// particular, --prompt "help" must never turn a request into help.
		if arg == "--help" || arg == "-h" || arg == "--h" {
			return args, true, nil
		}
		if local, _ := current.Metadata["local-help"].(bool); local && arg == "--all" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			// Traverse known root flags inherited by command groups. Their values
			// may themselves say "help"; skip those values without parsing them.
			if arg != "--" && (current == root || len(current.Commands) > 0) {
				name, _, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
				if flag := rootFlag(root, name); flag != nil {
					if local, ok := flag.(cli.LocalFlag); current != root && ok && local.IsLocal() {
						return args, false, nil
					}
					if doc, ok := flag.(cli.DocGenerationFlag); ok {
						if doc.TakesValue() && !assigned {
							i++
							if i >= len(args) {
								return args, false, nil
							}
						}
						continue
					}
				}
			}
			return args, false, nil
		}
		if arg == "" {
			continue
		}
		next := current.Command(arg)
		if next == nil {
			return args, false, nil
		}
		if current == root {
			commandStart = i
		}
		current = next
	}
	if local, _ := current.Metadata["local-help"].(bool); local {
		// These commands only display guidance. Let the framework's help path
		// skip request configuration, even when that configuration is broken.
		return append(append([]string(nil), args...), "--help"), true, nil
	}
	if len(current.Commands) > 0 && current.Action == nil {
		return append(append([]string(nil), args...), "--help"), true, nil
	}
	return args, false, nil
}

func rootFlag(root *cli.Command, name string) cli.Flag {
	for _, flag := range root.Flags {
		for _, alias := range flag.Names() {
			if alias == name {
				return flag
			}
		}
	}
	return nil
}

// Move authentic help switches behind the declared command path. The framework
// still parses every option and value; this scan never rewrites request values.
func normalizeHelpFlag(root *cli.Command, args []string) ([]string, bool) {
	chain := []*cli.Command{root}
	remove := map[int]bool{}
	help, legacy := false, false
	end := len(args)
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			end = i
			break
		}
		current := chain[len(chain)-1]
		spelling, value, assigned := strings.Cut(arg, "=")
		if spelling == "--help" || spelling == "-h" || spelling == "--h" {
			if assigned {
				enabled, err := strconv.ParseBool(value)
				if err != nil || !enabled {
					// Preserve false values, invalid values, and mixed true/false
					// flag precedence exactly as the framework handles them.
					return args, false
				}
			}
			remove[i], help = true, true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			name, value, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			var found cli.Flag
			for j := len(chain) - 1; j >= 0 && found == nil; j-- {
				for _, flag := range chain[j].Flags {
					if local, ok := flag.(cli.LocalFlag); j != len(chain)-1 && ok && local.IsLocal() {
						continue
					}
					for _, alias := range flag.Names() {
						if alias == name {
							found = flag
						}
					}
				}
			}
			if found == nil && current == root && (spelling == "--all" || spelling == "-all") {
				if assigned {
					if _, err := strconv.ParseBool(value); err != nil {
						return args, false
					}
				}
				remove[i], legacy = true, true
				continue
			}
			doc, ok := found.(cli.DocGenerationFlag)
			if !ok {
				return args, false
			}
			if doc.TakesValue() && !assigned {
				i++
				if i >= len(args) {
					return args, false
				}
			}
			continue
		}
		if arg == "help" && len(current.Commands) > 0 {
			// The dedicated help action retains its established topic parsing.
			return args, false
		}
		if arg == "" {
			continue
		}
		next := current.Command(arg)
		if next == nil {
			if len(current.Commands) == 0 && current != root {
				// Leaf operands are filenames or other request data. Keep them
				// intact while looking for a genuine help switch later on.
				continue
			}
			return args, false
		}
		chain = append(chain, next)
	}
	if !help {
		return args, false
	}
	out := make([]string, 0, len(args)+1)
	for i, arg := range args[:end] {
		if !remove[i] {
			out = append(out, arg)
		}
	}
	out = append(out, "--help")
	out = append(out, args[end:]...)
	root.Metadata["help-legacy-all"] = legacy
	root.Metadata["help-selected-command"] = chain[len(chain)-1]
	return out, true
}

var subcommandHelpOnce sync.Once

// urfave's group-help path does not consult CustomHelpTemplate. Honor it here
// as its command-help path does, retaining the existing renderer otherwise.
func installSubcommandHelp() {
	subcommandHelpOnce.Do(func() {
		printer := cli.HelpPrinter
		cli.HelpPrinter = func(writer io.Writer, template string, data any) {
			if command, ok := data.(*cli.Command); ok {
				if notice := legacyHelpNotice(command); notice != "" {
					_, _ = fmt.Fprintln(writer, notice)
				}
			}
			printer(writer, template, data)
		}
		commandHelp := cli.ShowCommandHelp
		cli.ShowCommandHelp = func(ctx context.Context, command *cli.Command, name string) error {
			target, selected := command.Root().Metadata["help-selected-command"].(*cli.Command)
			if !selected || target != command {
				return commandHelp(ctx, command, name)
			}
			// urfave interprets the first positional operand as another help
			// topic. A real help flag already selected this declared command.
			if command == command.Root() {
				return cli.ShowRootCommandHelp(command)
			}
			return commandHelp(ctx, command.Lineage()[1], command.Name)
		}
		fallback := cli.ShowSubcommandHelp
		cli.ShowSubcommandHelp = func(command *cli.Command) error {
			if _, configured := command.Root().Metadata["help-invocation"]; !configured || command.CustomHelpTemplate == "" {
				return fallback(command)
			}
			cli.HelpPrinter(command.Root().Writer, command.CustomHelpTemplate, command)
			return nil
		}
	})
}

func legacyHelpNotice(command *cli.Command) string {
	root := command.Root()
	invocation, configured := root.Metadata["help-invocation"].(string)
	if !configured {
		return ""
	}
	legacy, _ := root.Metadata["help-legacy-all"].(bool)
	if help := root.Command("help"); help != nil {
		legacy = legacy || help.IsSet("all")
	}
	if !legacy {
		return ""
	}
	var path []string
	for _, item := range command.Lineage() {
		if item != root && item.Name != "help" {
			path = append([]string{item.Name}, path...)
		}
	}
	return "Use " + strings.TrimSpace(invocation+" help "+strings.Join(path, " ")) + "; --all is no longer needed."
}

func showHelpTopics(ctx context.Context, command *cli.Command) error {
	root := command.Root()
	parent, target := root, root
	topics := command.Args().Slice()
	for i, topic := range topics {
		next := target.Command(topic)
		if next == nil || !allowsHelpTopic(next) || topic == "help" {
			return &UnknownTopicError{Parent: target, Topic: topic, Remaining: append([]string(nil), topics[i+1:]...)}
		}
		parent, target = target, next
	}
	if target == root {
		return cli.ShowRootCommandHelp(root)
	}
	return cli.ShowCommandHelp(ctx, parent, target.Name)
}

// UnknownTopicError retains the last valid group for safe local recovery guidance.
// Presenters can suggest declared commands without echoing an untrusted topic.
type UnknownTopicError struct {
	Parent    *cli.Command
	Topic     string
	Remaining []string
}

func (e *UnknownTopicError) Error() string {
	return fmt.Sprintf("Unknown help topic %q.", e.Topic)
}

func (*UnknownTopicError) ExitCode() int { return 3 }

// Compatibility aliases stay out of discovery but retain explicitly requested
// help. Internal commands remain unavailable as help topics.
func allowsHelpTopic(command *cli.Command) bool {
	compatibility, _ := command.Metadata["command-compatibility-alias"].(bool)
	return !command.Hidden || compatibility
}

// Invocation returns a copyable executable name for help examples without
// interpreting argv as shell syntax or displaying terminal controls.
func Invocation(fallback string, args []string) string {
	if len(args) == 0 || args[0] == "" {
		return fallback
	}
	name := args[0]
	if base := strings.TrimSuffix(filepath.Base(name), ".exe"); base != fallback {
		return fallback
	}
	// go run uses either a temporary executable or a cached executable under
	// <two hex digits>/<64 hex digits>-d. Neither path is a stable invocation.
	dir := filepath.Dir(name)
	cacheKey, cachedGoRun := strings.CutSuffix(filepath.Base(dir), "-d")
	cachedGoRun = cachedGoRun && len(cacheKey) == 64 && strings.Trim(cacheKey, "0123456789abcdef") == "" && filepath.Base(filepath.Dir(dir)) == cacheKey[:2]
	temporaryGoRun := filepath.Base(dir) == "exe" && strings.HasPrefix(filepath.Base(filepath.Dir(dir)), "b") && strings.Contains(filepath.ToSlash(name), "/go-build")
	if temporaryGoRun || cachedGoRun {
		if _, err := os.Stat("cmd/" + fallback + "/main.go"); err == nil {
			return "go run ./cmd/" + fallback
		}
		return fallback
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fallback
	}
	// Installed commands need no directory prefix when PATH selects this same
	// executable. Keep the invoked path for local builds or other installations.
	// Relative matches can come from Go's implicit current-directory lookup
	// when execerrdot=0, which does not match PowerShell's command lookup.
	if installed, err := exec.LookPath(fallback); err == nil && filepath.IsAbs(installed) &&
		sameExecutable(name, installed) && !hasPowerShellScriptOnPath(fallback) {
		return fallback
	}
	// PowerShell can supply an absolute argv[0] even for .\openai.exe. When
	// already in the executable's folder, use the relative form accepted by
	// both PowerShell and cmd.exe, including folders containing spaces.
	if filepath.IsAbs(name) {
		if local, err := filepath.Abs(filepath.Base(name)); err == nil && sameExecutable(name, local) {
			if runtime.GOOS == "windows" {
				return `.\` + filepath.Base(name)
			}
			return "./" + filepath.Base(name)
		}
	}
	needsQuoting := strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._:-", r) || runtime.GOOS == "windows" && r == '\\')
	}) >= 0
	if needsQuoting {
		return quoteInvocation(name, fallback)
	}
	return name
}

func hasPowerShellScriptOnPath(name string) bool {
	// PowerShell can discover .ps1 commands that exec.LookPath misses, including
	// scripts without a Unix executable bit and dangling symlinks. Keep the full
	// path whenever one is present, since shell command precedence differs.
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if info, err := os.Lstat(filepath.Join(directory, name+".ps1")); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func sameExecutable(first, second string) bool {
	a, err := os.Stat(first)
	if err != nil {
		return false
	}
	b, err := os.Stat(second)
	return err == nil && os.SameFile(a, b)
}
