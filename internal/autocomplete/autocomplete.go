package autocomplete

import (
	"context"
	"embed"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/readable"
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
	if cmd.Bool("picker") {
		picker, err := renderPickerCompletion(s, cmd.Root().Name)
		if err != nil {
			return cli.Exit(err, 1)
		}
		completionScript += "\n" + picker
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
	ShellCompletionBehaviorDefault    ShellCompletionBehavior = iota
	ShellCompletionBehaviorFile                               = 10
	ShellCompletionBehaviorNoComplete                         = 11
)

type CompletionResult struct {
	Completions []ShellCompletion
	Behavior    ShellCompletionBehavior
	// FileValuePrefix is emitted only for an assigned file value. Shells use it
	// to distinguish --file=path from a separated path that contains an equals.
	FileValuePrefix string
	// Older adapters cannot preserve paths for newly enabled file values.
	requiresFileValueSupport bool
	// Older Bash adapters cannot restore wholly quoted assignment prefixes.
	requiresStaticValueSupport bool
}

func isFlag(arg string) bool {
	return strings.HasPrefix(arg, "-")
}

func findFlag(flags []cli.Flag, arg string) *cli.Flag {
	name := strings.TrimLeft(arg, "-")
	for _, flag := range flags {
		if vf, ok := flag.(cli.VisibleFlag); ok && !vf.IsVisible() {
			continue
		}

		if slices.Contains(flag.Names(), name) {
			return &flag
		}
	}
	return nil
}

// Completion walks commands before their parent links are initialized. Keep
// the lineage explicitly to include persistent flags and respect local aliases.
func completionFlags(lineage []*cli.Command) []cli.Flag {
	var flags []cli.Flag
	seen := make(map[string]bool)
	for i := len(lineage) - 1; i >= 0; i-- {
		for _, flag := range lineage[i].Flags {
			if i != len(lineage)-1 {
				local, ok := flag.(cli.LocalFlag)
				if !ok || local.IsLocal() || slices.ContainsFunc(flag.Names(), func(name string) bool { return seen[name] }) {
					continue
				}
			}
			flags = append(flags, flag)
			for _, name := range flag.Names() {
				seen[name] = true
			}
		}
	}
	return flags
}

func findChild(cmd *cli.Command, name string) *cli.Command {
	for _, c := range cmd.Commands {
		// Hidden compatibility routes are still valid when explicitly typed.
		// Prefix completion below requires a colon to expose these names.
		compatibility, _ := c.Metadata["command-compatibility-alias"].(bool)
		if (!c.Hidden || compatibility) && slices.Contains(c.Names(), name) {
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
		for _, child := range completionCommands(root) {
			completions = builder.createFromCommand("", child, completions)
		}
		return CompletionResult{Completions: completions, Behavior: ShellCompletionBehaviorDefault}
	}

	current := args[len(args)-1]
	preceding := args[0 : len(args)-1]
	cmd := root
	lineage := []*cli.Command{root}
	flags := completionFlags(lineage)
	help := root.Command("help")
	localHelp := false
	if help != nil {
		localHelp, _ = help.Metadata["help-topic-command"].(bool)
	}
	helpTopics, literal := false, false
	positionalCount := 0
	var usedFlags []cli.Flag
	i := 0
	for i < len(preceding) {
		arg := preceding[i]

		if arg == "--" && !literal {
			literal = true
			i++
			continue
		}
		if isFlag(arg) && !literal {
			name, value, assigned := strings.Cut(arg, "=")
			flag := findFlag(flags, name)
			// Accept the typed legacy help flag without offering it in discovery.
			if flag == nil && helpTopics && (name == "--all" || name == "-all") {
				for _, declared := range help.Flags {
					legacy, ok := declared.(*cli.BoolFlag)
					if !ok || legacy.Name != "all" || !slices.Contains(flags, declared) {
						continue
					}
					if assigned {
						if _, err := strconv.ParseBool(value); err != nil {
							break
						}
					}
					flag = &declared
					break
				}
			}
			if flag == nil {
				return CompletionResult{Behavior: ShellCompletionBehaviorNoComplete}
			}
			usedFlags = append(usedFlags, *flag)
			if docFlag, ok := (*flag).(cli.DocGenerationFlag); ok && docFlag.TakesValue() && !assigned {
				if i == len(preceding)-1 {
					return flagValueCompletion(root, cmd, *flag, unquoteStaticValue(current, completionStyle), "", completionStyle)
				}
				i += 2
			} else {
				i++
			}
		} else {
			fileFlag, _ := cmd.Metadata["completion-positional-file"].(string)
			if fileFlag != "" && !helpTopics && (literal || arg == "") {
				positionalCount++
				i++
				continue
			}
			if arg == "" {
				i++
				continue
			}
			if localHelp && arg == "help" && !helpTopics && len(cmd.Commands) > 0 {
				helpTopics = true
				flags = completionFlags([]*cli.Command{root, help})
				i++
				continue
			}
			child := findChild(cmd, arg)
			if helpTopics && cmd == root && arg == "setup" {
				child = findChild(help, arg)
			}
			if helpTopics && arg == "help" {
				child = nil
			}
			if child != nil {
				cmd = child
				positionalCount = 0
				lineage = append(lineage, child)
				if !helpTopics {
					flags = completionFlags(lineage)
				}
			} else if helpTopics || len(cmd.Commands) > 0 {
				// A failed group traversal must not offer commands from its parent.
				return CompletionResult{Behavior: ShellCompletionBehaviorNoComplete}
			} else {
				positionalCount++
			}
			i++
		}
	}

	// Some shells retain quotes around the current argument. Only opt known
	// static values into this path; file and free-form completion stay unchanged.
	if !literal {
		if unquoted := unquoteStaticValue(current, completionStyle); unquoted != current && isFlag(unquoted) {
			if name, value, assigned := strings.Cut(unquoted, "="); assigned {
				if flag := findFlag(flags, name); flag != nil {
					if doc, ok := (*flag).(cli.DocGenerationFlag); ok && doc.TakesValue() {
						// Quotes inside the outer argument are literal value data.
						result := flagValueCompletion(root, cmd, *flag, value, name+"=", completionStyle)
						if len(result.Completions) != 0 {
							return result
						}
					}
				}
			}
		}
	}

	// Complete assigned values before matching flag names.
	if isFlag(current) && !literal {
		if name, value, assigned := strings.Cut(current, "="); assigned {
			result := CompletionResult{Behavior: ShellCompletionBehaviorNoComplete}
			if flag := findFlag(flags, name); flag != nil {
				if doc, ok := (*flag).(cli.DocGenerationFlag); ok && doc.TakesValue() {
					result = flagValueCompletion(root, cmd, *flag, unquoteStaticValue(value, completionStyle), name+"=", completionStyle)
					if result.Behavior == ShellCompletionBehaviorFile {
						result.FileValuePrefix = name + "="
						result.requiresFileValueSupport = true
					}
				}
			}
			return result
		}
	}

	// Completing a flag name
	if isFlag(current) && !literal {
		for _, flag := range flags {
			if vf, ok := flag.(cli.VisibleFlag); ok && !vf.IsVisible() {
				continue
			}
			completions = builder.createFromFlag(current, &flag, completions)
		}
	}

	// Metadata opts one positional path into its existing alternate file flag.
	// Keep flag-value completion above and help-topic traversal unchanged.
	fileFlag, _ := cmd.Metadata["completion-positional-file"].(string)
	if fileFlag != "" && !helpTopics && (literal || !isFlag(current)) {
		if positionalCount == 0 {
			if flag := findFlag(flags, fileFlag); flag != nil && !slices.Contains(usedFlags, *flag) {
				result := flagValueCompletion(root, cmd, *flag, current, "", completionStyle)
				if result.Behavior == ShellCompletionBehaviorFile {
					result.requiresFileValueSupport = true
					return result
				}
			}
		}
		return CompletionResult{Behavior: ShellCompletionBehaviorNoComplete}
	}

	// Keep compatibility aliases out of discovery unless a colon requests them.
	colonPrefix := strings.Contains(current, ":")
	children := completionCommands(cmd)
	if helpTopics && cmd == root {
		children = slices.DeleteFunc(children, func(child *cli.Command) bool { return child == help })
		children = append(children, clihelp.VisibleCommands(help)...)
	}
	for _, child := range children {
		completions = builder.createFromCommand(current, child, completions)
	}
	for _, child := range cmd.Commands {
		compatibility, _ := child.Metadata["command-compatibility-alias"].(bool)
		if child.Hidden && compatibility && colonPrefix {
			completions = builder.createFromCommand(current, child, completions)
		}
	}

	return CompletionResult{
		Completions: completions,
		Behavior:    ShellCompletionBehaviorDefault,
	}
}

func flagValueCompletion(root, selected *cli.Command, flag cli.Flag, prefix, assignment string, style CompletionStyle) CompletionResult {
	values, _ := root.Metadata["completion-flag-values"].(map[cli.Flag][]string)
	choices := values[flag]
	// Overrides apply only to this selected command and the actual root flag.
	// A same-name local flag must keep its own completion contract.
	overrides, _ := selected.Metadata["completion-root-flag-values"].(map[string][]string)
	for _, rootFlag := range root.Flags {
		if rootFlag == flag {
			if override, ok := overrides[rootFlag.Names()[0]]; ok {
				choices = override
			}
			break
		}
	}
	var completions []ShellCompletion
	for _, value := range choices {
		if strings.HasPrefix(value, prefix) {
			name := value
			// Readline replaces the word after '='. Other adapters replace the
			// complete assigned argument through their existing value protocol.
			if style != CompletionStyleBash {
				name = assignment + name
			}
			completions = append(completions, NewShellCompletion(name, ""))
		}
	}
	if len(completions) != 0 {
		return CompletionResult{
			Completions:                completions,
			requiresStaticValueSupport: style == CompletionStyleBash && assignment != "",
		}
	}
	if wrapped, ok := flag.(interface{ CLIStringFlag() *cli.StringFlag }); ok {
		flag = wrapped.CLIStringFlag()
	}
	if file, ok := flag.(interface{ IsFileInput() bool }); ok && file.IsFileInput() {
		return CompletionResult{Behavior: ShellCompletionBehaviorFile, requiresFileValueSupport: true}
	}
	if file, ok := flag.(*cli.StringFlag); ok && file.TakesFile {
		return CompletionResult{Behavior: ShellCompletionBehaviorFile}
	}
	return CompletionResult{Behavior: ShellCompletionBehaviorNoComplete}
}

// Zsh and Fish retain current-word quotes. Bash and PowerShell already decode
// that layer. Remove it once for static matching, without evaluating syntax.
func unquoteStaticValue(value string, style CompletionStyle) string {
	if style != CompletionStyleZsh && style != CompletionStyleFish ||
		len(value) == 0 || value[0] != '\'' && value[0] != '"' {
		return value
	}
	quote := value[:1]
	return strings.TrimSuffix(value[1:], quote)
}

func completionCommands(command *cli.Command) []*cli.Command {
	children := clihelp.VisibleCommands(command)
	// The framework omits help from VisibleCommands. Preserve its completion
	// aliases without adding framework-specific helpers to the printed index.
	if help := command.Command("help"); help != nil && !help.Hidden {
		children = append(children, help)
	}
	return children
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

	// Bash/fish pass a separator. Zsh sends only the user's tokens, so its
	// leading -- may be a flag prefix or the user's end-of-options marker.
	// Keep the separator form accepted by direct PowerShell backend callers.
	if completionStyle != CompletionStyleZsh && len(args) > 0 && args[0] == "--" {
		args = args[1:]
	} else if completionStyle == CompletionStylePowershell && len(args) > 1 {
		name := strings.ReplaceAll(strings.Trim(args[0], `"'`), `\`, "/")
		name = name[strings.LastIndex(name, "/")+1:]
		if strings.EqualFold(strings.TrimSuffix(strings.ToLower(name), ".exe"), root.Name) {
			args = args[1:]
		}
	}

	result := GetCompletions(completionStyle, root, args)
	// Adapters advertise this only for their backend call. Older binaries ignore
	// the marker; older loaded adapters keep their existing completion behavior.
	if result.requiresFileValueSupport && os.Getenv("OPENAI_CLI_COMPLETION_FILE_VALUES") != "1" {
		result = CompletionResult{Behavior: ShellCompletionBehaviorNoComplete}
	}
	if result.requiresStaticValueSupport && os.Getenv("OPENAI_CLI_COMPLETION_STATIC_VALUES") != "1" {
		result = CompletionResult{Behavior: ShellCompletionBehaviorNoComplete}
	}
	if result.FileValuePrefix != "" {
		if _, err := fmt.Fprintln(cmd.Writer, result.FileValuePrefix); err != nil {
			return err
		}
	}

	for _, completion := range result.Completions {
		name := completion.Name
		if completionStyle == CompletionStyleZsh {
			name = strings.ReplaceAll(name, ":", "\\:")
		}
		// Shell adapters split on newlines and tabs. Keep API prose within one
		// record and escape terminal controls before they reach the menu.
		usage := readable.Text(strings.Join(strings.Fields(completion.Usage), " "))
		if sentence, _, found := strings.Cut(usage, ". "); found {
			usage = sentence + "."
		}
		if completionStyle == CompletionStyleZsh && usage != "" {
			name += ":" + usage
		} else if completionStyle == CompletionStyleFish && usage != "" {
			name += "\t" + usage
		}
		if _, err := fmt.Fprintln(cmd.Writer, name); err != nil {
			return err
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
