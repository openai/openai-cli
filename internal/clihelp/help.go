// Package clihelp owns local onboarding and help-topic routing. Feature commands
// supply their own brief and full templates through command metadata.
package clihelp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"

	"github.com/urfave/cli/v3"
)

const welcomeHelp = `{{$run := index .Root.Metadata "help-invocation"}}OpenAI CLI
Use the OpenAI API from your terminal.

START HERE
  {{$run}} help setup                  Set up your API key
  {{$run}} models list                 List models available to your key

GET HELP
  {{$run}} --help                      This menu (also -h)
  {{$run}} images --help               Browse image commands
  {{$run}} images generate --help      Example and common inputs
  {{$run}} help --all                  Every command and global option
  {{$run}} help --all images generate  Complete help for one command

Add --help (or -h) to any command. Help needs no API key or internet.
`

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
// Commands marked "local-help" only display guidance; "local-help-full" supplies
// an optional full reference template used by help --all.
func Configure(root *cli.Command, args []string) ([]string, bool, error) {
	installSubcommandHelp()
	root.CustomRootCommandHelpTemplate = welcomeHelp
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}
	root.Metadata["help-invocation"] = Invocation(root.Name, args)
	configureCommandHelp(root, root.Metadata["help-invocation"].(string), "")
	if root.Command("help") == nil {
		root.Commands = append(root.Commands, &cli.Command{
			Name: "help", Usage: "Get help: help [--all] [command...]", HideHelpCommand: true,
			CustomHelpTemplate: welcomeHelp,
			Flags:              []cli.Flag{&cli.BoolFlag{Name: "all", Usage: "Show every command option", HideDefault: true}},
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
	if len(args) <= 1 {
		return []string{root.Name, "--help"}, true, nil
	}
	if args[1] == "__complete" {
		return args, false, nil
	}
	current := root
	for i := 1; i < len(args); i++ {
		arg := args[i]
		// On a leaf, "help" can be a filename or another positional operand.
		// Only resource groups accept the help-command shorthand.
		if arg == "help" && len(current.Commands) > 0 {
			if current == root {
				// Let the framework dispatch root help and its setup guide, including
				// options before or after the topic. Normalize only nested shorthand.
				out := append([]string(nil), args[:i+1]...)
				literal := false
				for _, topic := range args[i+1:] {
					literal = literal || topic == "--"
					if literal || topic != "--help" && topic != "-h" && topic != "--h" {
						out = append(out, topic)
					}
				}
				return out, true, nil
			}
			path, all, literal := []string{}, false, false
			for _, topic := range args[i+1:] {
				if !literal {
					switch topic {
					case "--":
						literal = true
						continue
					case "--all":
						all = true
						continue
					case "--help", "-h", "--h":
						continue
					}
				}
				next := current.Command(topic)
				if next == nil || next.Hidden || topic == "help" {
					return nil, true, cli.Exit(fmt.Sprintf("Unknown help topic %q. Run %s help --all to see commands.", topic, root.Metadata["help-invocation"]), 3)
				}
				current = next
				path = append(path, topic)
			}
			if all {
				useFullHelp(root, current)
			}
			out := append(append([]string(nil), args[:i]...), path...)
			return append(out, "--help"), true, nil
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

var subcommandHelpOnce sync.Once

// urfave's group-help path does not consult CustomHelpTemplate. Honor it here
// as its command-help path does, retaining the existing renderer otherwise.
func installSubcommandHelp() {
	subcommandHelpOnce.Do(func() {
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

func showHelpTopics(ctx context.Context, command *cli.Command) error {
	root := command.Root()
	parent, target := root, root
	for _, topic := range command.Args().Slice() {
		next := target.Command(topic)
		if next == nil || next.Hidden || topic == "help" {
			return cli.Exit(fmt.Sprintf("Unknown help topic %q. Run %s help --all to see commands.", topic, root.Metadata["help-invocation"]), 3)
		}
		parent, target = target, next
	}
	if command.Bool("all") {
		useFullHelp(root, target)
	}
	if target == root {
		return cli.ShowRootCommandHelp(root)
	}
	return cli.ShowCommandHelp(ctx, parent, target.Name)
}

func useFullHelp(root, target *cli.Command) {
	if full, _ := target.Metadata["local-help-full"].(string); full != "" {
		target.CustomHelpTemplate = full
	} else if target == root {
		root.CustomRootCommandHelpTemplate = fullHelpTemplate(root, cli.RootCommandHelpTemplate)
	} else if len(target.Commands) > 0 {
		target.CustomHelpTemplate = fullHelpTemplate(target, cli.SubcommandHelpTemplate)
	} else {
		target.CustomHelpTemplate = fullHelpTemplate(target, cli.CommandHelpTemplate)
	}
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
	// go run builds an executable in a temporary go-build directory. That
	// executable disappears; repeat the user's source invocation instead.
	if filepath.Base(filepath.Dir(name)) == "exe" && strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(name))), "b") && strings.Contains(filepath.ToSlash(name), "/go-build") {
		if _, err := os.Stat("cmd/" + fallback + "/main.go"); err == nil {
			return "go run ./cmd/" + fallback
		}
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
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

func sameExecutable(first, second string) bool {
	a, err := os.Stat(first)
	if err != nil {
		return false
	}
	b, err := os.Stat(second)
	return err == nil && os.SameFile(a, b)
}
