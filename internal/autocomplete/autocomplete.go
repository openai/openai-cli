package autocomplete

import (
	"context"
	"embed"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

type CompletionStyle string

const (
	CompletionStyleZsh        CompletionStyle = "zsh"
	CompletionStyleBash       CompletionStyle = "bash"
	CompletionStylePowershell CompletionStyle = "pwsh"
	CompletionStyleFish       CompletionStyle = "fish"
)

type renderCompletion func(cmd *cli.Command, appName string) (string, error)

var (
	//go:embed shellscripts
	autoCompleteFS embed.FS

	shellCompletions = map[CompletionStyle]renderCompletion{
		"bash": func(c *cli.Command, appName string) (string, error) {
			b, err := autoCompleteFS.ReadFile("shellscripts/bash_autocomplete.bash")
			return strings.ReplaceAll(string(b), "__APPNAME__", appName), err
		},
		"fish": func(c *cli.Command, appName string) (string, error) {
			b, err := autoCompleteFS.ReadFile("shellscripts/fish_autocomplete.fish")
			return strings.ReplaceAll(string(b), "__APPNAME__", appName), err
		},
		"pwsh": func(c *cli.Command, appName string) (string, error) {
			b, err := autoCompleteFS.ReadFile("shellscripts/pwsh_autocomplete.ps1")
			return strings.ReplaceAll(string(b), "__APPNAME__", appName), err
		},
		"zsh": func(c *cli.Command, appName string) (string, error) {
			b, err := autoCompleteFS.ReadFile("shellscripts/zsh_autocomplete.zsh")
			return strings.ReplaceAll(string(b), "__APPNAME__", appName), err
		},
	}
)

func OutputCompletionScript(ctx context.Context, cmd *cli.Command) error {
	shells := make([]CompletionStyle, 0, len(shellCompletions))
	for k := range shellCompletions {
		shells = append(shells, k)
	}

	if cmd.Args().Len() == 0 {
		return cli.Exit(fmt.Sprintf("no shell provided for completion command. available shells are %+v", shells), 1)
	}
	s := CompletionStyle(cmd.Args().First())

	renderCompletion, ok := shellCompletions[s]
	if !ok {
		return cli.Exit(fmt.Sprintf("unknown shell %s, available shells are %+v", s, shells), 1)
	}

	completionScript, err := renderCompletion(cmd, cmd.Root().Name)
	if err != nil {
		return cli.Exit(err, 1)
	}

	_, err = cmd.Writer.Write([]byte(completionScript))
	if err != nil {
		return cli.Exit(err, 1)
	}

	return nil
}

type ShellCompletion struct {
	Name  string
	Usage string
}

func NewShellCompletion(name string, usage string) ShellCompletion {
	return ShellCompletion{Name: name, Usage: usage}
}

type ShellCompletionBehavior int

const (
	ShellCompletionBehaviorDefault ShellCompletionBehavior = iota
	ShellCompletionBehaviorFile                            = 10
	ShellCompletionBehaviorNoComplete
)

type CompletionResult struct {
	Completions []ShellCompletion
	Behavior    ShellCompletionBehavior
}

func isFlag(arg string) bool {
	return strings.HasPrefix(arg, "-")
}

func findFlag(cmd *cli.Command, arg string) *cli.Flag {
	name := strings.TrimLeft(arg, "-")
	for _, flag := range cmd.Flags {
		if vf, ok := flag.(cli.VisibleFlag); ok && !vf.IsVisible() {
			continue
		}

		if slices.Contains(flag.Names(), name) {
			return &flag
		}
	}
	return nil
}

func findChild(cmd *cli.Command, name string) *cli.Command {
	for _, c := range cmd.Commands {
		if !c.Hidden && c.Name == name {
			return c
		}
	}
	return nil
}

type shellCompletionBuilder struct {
	completionStyle CompletionStyle
}

func (scb *shellCompletionBuilder) createFromCommand(input string, command *cli.Command, result []ShellCompletion) []ShellCompletion {
	matchingNames := make([]string, 0, len(command.Names()))

	for _, name := range command.Names() {
		if strings.HasPrefix(name, input) {
			matchingNames = append(matchingNames, name)
		}
	}

	if scb.completionStyle == CompletionStyleBash {
		index := strings.LastIndex(input, ":") + 1
		if index > 0 {
			for _, name := range matchingNames {
				result = append(result, NewShellCompletion(name[index:], command.Usage))
			}
			return result
		}
	}

	for _, name := range matchingNames {
		result = append(result, NewShellCompletion(name, command.Usage))
	}
	return result
}

func (scb *shellCompletionBuilder) createFromFlag(input string, flag *cli.Flag, result []ShellCompletion) []ShellCompletion {
	matchingNames := make([]string, 0, len((*flag).Names()))

	for _, name := range (*flag).Names() {
		withPrefix := ""
		if len(name) == 1 {
			withPrefix = "-" + name
		} else {
			withPrefix = "--" + name
		}

		if strings.HasPrefix(withPrefix, input) {
			matchingNames = append(matchingNames, withPrefix)
		}
	}

	usage := ""
	if dgf, ok := (*flag).(cli.DocGenerationFlag); ok {
		usage = dgf.GetUsage()
	}

	for _, name := range matchingNames {
		result = append(result, NewShellCompletion(name, usage))
	}

	return result
}

func GetCompletions(completionStyle CompletionStyle, root *cli.Command, args []string) CompletionResult {
	result := getAllPossibleCompletions(completionStyle, root, args)

	// If the user has not put in a colon, filter out colon commands
	if len(args) > 0 && !strings.Contains(args[len(args)-1], ":") {
		// Nothing with anything after a colon. Create a single entry for groups with the same colon subset
		foundNames := make([]string, 0, len(result.Completions))
		filteredCompletions := make([]ShellCompletion, 0, len(result.Completions))

		for _, completion := range result.Completions {
			name := completion.Name
			firstColonIndex := strings.Index(name, ":")
			if firstColonIndex > -1 {
				name = name[0:firstColonIndex]
				completion.Name = name
				completion.Usage = ""
			}

			if !slices.Contains(foundNames, name) {
				foundNames = append(foundNames, name)
				filteredCompletions = append(filteredCompletions, completion)
			}
		}

		result.Completions = filteredCompletions
	}

	return result
}

func getAllPossibleCompletions(completionStyle CompletionStyle, root *cli.Command, args []string) CompletionResult {
	builder := shellCompletionBuilder{completionStyle: completionStyle}
	completions := make([]ShellCompletion, 0)
	if len(args) == 0 {
		for _, child := range root.Commands {
			completions = builder.createFromCommand("", child, completions)
		}
		return CompletionResult{Completions: completions, Behavior: ShellCompletionBehaviorDefault}
	}

	current := args[len(args)-1]
	preceding := args[0 : len(args)-1]
	cmd := root
	i := 0
	for i < len(preceding) {
		arg := preceding[i]

		if isFlag(arg) {
			flag := findFlag(cmd, arg)
			if flag == nil {
				i++
			} else if docFlag, ok := (*flag).(cli.DocGenerationFlag); ok && docFlag.TakesValue() {
				// All flags except for bool flags take values
				i += 2
			} else {
				i++
			}
		} else {
			child := findChild(cmd, arg)
			if child != nil {
				cmd = child
			}
			i++
		}
	}

	// Check if the previous arg was a flag expecting a value
	if len(preceding) > 0 {
		prev := preceding[len(preceding)-1]
		if isFlag(prev) {
			flag := findFlag(cmd, prev)
			if flag != nil {
				if fb, ok := (*flag).(*cli.StringFlag); ok && fb.TakesFile {
					return CompletionResult{Completions: completions, Behavior: ShellCompletionBehaviorFile}
				} else if docFlag, ok := (*flag).(cli.DocGenerationFlag); ok && docFlag.TakesValue() {
					return CompletionResult{Completions: completions, Behavior: ShellCompletionBehaviorNoComplete}
				}
			}
		}
	}

	// Completing a flag name
	if isFlag(current) {
		for _, flag := range cmd.Flags {
			completions = builder.createFromFlag(current, &flag, completions)
		}
	}

	for _, child := range cmd.Commands {
		if !child.Hidden {
			completions = builder.createFromCommand(current, child, completions)
		}
	}

	return CompletionResult{
		Completions: completions,
		Behavior:    ShellCompletionBehaviorDefault,
	}
}

func ExecuteShellCompletion(ctx context.Context, cmd *cli.Command) error {
	root := cmd.Root()
	args := rebuildColonSeparatedArgs(root.Args().Slice()[1:])

	var completionStyle CompletionStyle
	if style, ok := os.LookupEnv("COMPLETION_STYLE"); ok {
		switch style {
		case "bash":
			completionStyle = CompletionStyleBash
		case "zsh":
			completionStyle = CompletionStyleZsh
		case "pwsh":
			completionStyle = CompletionStylePowershell
		case "fish":
			completionStyle = CompletionStyleFish
		default:
			return cli.Exit("COMPLETION_STYLE must be set to 'bash', 'zsh', 'pwsh', or 'fish'", 1)
		}
	} else {
		return cli.Exit("COMPLETION_STYLE must be set to 'bash', 'zsh', 'pwsh', 'fish'", 1)
	}

	result := GetCompletions(completionStyle, root, args)

	for _, completion := range result.Completions {
		name := completion.Name
		if completionStyle == CompletionStyleZsh {
			name = strings.ReplaceAll(name, ":", "\\:")
		}
		if completionStyle == CompletionStyleZsh && len(completion.Usage) > 0 {
			_, _ = fmt.Fprintf(cmd.Writer, "%s:%s\n", name, completion.Usage)
		} else if completionStyle == CompletionStyleFish && len(completion.Usage) > 0 {
			_, _ = fmt.Fprintf(cmd.Writer, "%s\t%s\n", name, completion.Usage)
		} else {
			_, _ = fmt.Fprintf(cmd.Writer, "%s\n", name)
		}
	}
	return cli.Exit("", int(result.Behavior))
}

// When CLI arguments are passed in, they are separated on word barriers.
// Most commonly this is whitespace but in some cases that may also be colons.
// We wish to allow arguments with colons. To handle this, we append/prepend colons to their neighboring
// arguments.
//
// Example: `rebuildColonSeparatedArgs(["a", "b", ":", "c", "d"])` => `["a", "b:c", "d"]`
func rebuildColonSeparatedArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}

	result := []string{}
	i := 0

	for i < len(args) {
		current := args[i]

		// Keep joining while the next element is ":" or the current element ends with ":"
		for i+1 < len(args) && (args[i+1] == ":" || strings.HasSuffix(current, ":")) {
			if args[i+1] == ":" {
				current += ":"
				i++
				// Check if there's a following element after the ":"
				if i+1 < len(args) && args[i+1] != ":" {
					current += args[i+1]
					i++
				}
			} else {
				break
			}
		}

		result = append(result, current)
		i++
	}

	return result
}
