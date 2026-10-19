package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

type errorCoverageFlag struct {
	Name               string
	Aliases            []string
	Type               string
	TakesValue         bool
	Required           bool
	RequiredAfterInput bool
	Owner              string
	Persistent         bool
	Hidden             bool
	Policy             string
	Origin             string
}

type errorCoverageCommand struct {
	Path           []string
	Aliases        []string
	Compatibility  bool
	HasChildren    bool
	Flags          []errorCoverageFlag
	Effective      []errorCoverageFlag
	EffectiveNames map[string]string
	Shadowed       []string
	Class          string
	ActionContract string
}

type errorCoverageManifest struct {
	Commands   []errorCoverageCommand
	Exclusions map[string]string
	Groups     []errorCoverageGroup
}

type errorCoverageGroup struct {
	Key            string
	Flag           errorCoverageFlag
	Scopes         [][]string
	Representative []string
}

// This policy describes parser syntax, not endpoint-specific accepted values.
// Unknown future declaration types must gain a reviewed policy and probe.
func errorCoverageTypePolicy(name string) (string, bool) {
	switch name {
	case "int", "uint", "float", "bool", "boolean", "duration", "date", "datetime", "time":
		return "typed invalid value", true
	case "string", "any", "":
		return "no universally invalid scalar; missing values and request decoding cover syntax", true
	case "string=any":
		return "object fields accept arbitrary values; missing values and request decoding cover syntax", true
	default:
		return "", false
	}
}

// Match the framework-owned booleans before modeling setup. A dependency or
// application change must update this policy instead of silently losing probes.
func errorCoverageFrameworkFlag(flag cli.Flag, name, alias, owner string) (errorCoverageFlag, error) {
	boolean, ok := flag.(*cli.BoolFlag)
	if !ok || boolean == nil || !slices.Equal(boolean.Names(), []string{name, alias}) ||
		boolean.TypeName() != "bool" || !boolean.IsLocal() || boolean.TakesValue() || boolean.Hidden {
		return errorCoverageFlag{}, fmt.Errorf("classify framework %s flag: expected local visible boolean --%s/-%s", name, name, alias)
	}
	return errorCoverageFlag{Name: name, Aliases: []string{alias}, Type: "bool", Owner: owner,
		Origin: "urfave " + name, Policy: "framework local boolean; invalid values retain parser guidance"}, nil
}

func errorCoverageFrameworkDeclarations(command *cli.Command, path string, root, hideHelp bool, declared []errorCoverageFlag) ([]errorCoverageFlag, error) {
	help, err := errorCoverageFrameworkFlag(cli.HelpFlag, "help", "h", path)
	if err != nil {
		return nil, err
	}
	version, err := errorCoverageFrameworkFlag(cli.VersionFlag, "version", "v", path)
	if err != nil {
		return nil, err
	}
	claimed := map[string]bool{}
	for _, flag := range declared {
		for _, name := range append([]string{flag.Name}, flag.Aliases...) {
			claimed[name] = true
		}
	}
	var flags []errorCoverageFlag
	if !hideHelp {
		if claimed["help"] || claimed["h"] {
			return nil, fmt.Errorf("classify framework help collision on command %q before adding help probes", path)
		}
		flags = append(flags, help)
		claimed["help"], claimed["h"] = true, true
	}
	// urfave disables version for empty Version, injects it only on the root,
	// and removes aliases already claimed by an application declaration.
	if root && !command.HideVersion && command.Version != "" {
		version.Aliases = slices.DeleteFunc(version.Aliases, func(alias string) bool { return claimed[alias] && alias != version.Name })
		if !claimed[version.Name] {
			flags = append(flags, version)
		}
	}
	return flags, nil
}

// Build from declarations only. Do not Run, parse, alter flags, or execute
// actions: a caller can safely audit synthetic and production registrations.
func buildErrorCoverageManifest(root *cli.Command) (errorCoverageManifest, error) {
	manifest := errorCoverageManifest{Exclusions: map[string]string{}}
	if root == nil {
		return manifest, fmt.Errorf("cannot audit a nil command tree")
	}
	internalContracts := map[string]string{
		"__complete":          "Completion protocol disables ordinary parsing; native completion tests cover it.",
		"tokenizer __preview": "Isolated tokenizer worker protocol; tokenizer worker tests cover it.",
		"tokenizer __output":  "Isolated terminal output protocol; terminal output tests cover it.",
	}
	hiddenUtilities := map[string]string{
		"@completion": "Parser cases here; action failures use TestMainRecoveryCompletionSetup.",
		"@manpages":   "Parser cases here; destination failures use TestMainRecoveryManpageOutput.",
	}
	hiddenFlags := map[string]string{
		"help/--all":                                       "Deprecated help migration flag; help compatibility tests cover valid usage.",
		"admin certificates create/--content":              "Hidden compatibility field; explicit-use errors retain its declared name, but suggestions omit it.",
		"admin organization certificates create/--content": "Hidden compatibility field; explicit-use errors retain its declared name, but suggestions omit it.",
		"admin:organization:certificates create/--content": "Hidden compatibility field; explicit-use errors retain its declared name, but suggestions omit it.",
	}
	groups := map[string]*errorCoverageGroup{}
	var visit func(*cli.Command, []string, bool, [][]errorCoverageFlag, bool) error
	visit = func(command *cli.Command, path []string, compatibility bool, ancestors [][]errorCoverageFlag, inheritedHideHelp bool) error {
		hideHelp := inheritedHideHelp || command.HideHelp
		name := strings.Join(path, " ")
		marked, _ := command.Metadata["command-compatibility-alias"].(bool)
		compatibility = compatibility || marked
		class := "public"
		actionContract := ""
		if command.Hidden && !marked {
			if reason, exists := internalContracts[name]; exists {
				manifest.Exclusions[name] = reason
				return nil
			}
			if reason, exists := hiddenUtilities[name]; exists {
				class = "hidden utility: " + reason
				actionContract = name
			} else {
				return fmt.Errorf("classify hidden command %q and name its executable contract tests", name)
			}
		}
		if command.AllowExtFlags {
			return fmt.Errorf("classify external runtime flags on command %q before inventorying their error contracts", name)
		}
		if command.SkipFlagParsing || command.StopOnNthArg != nil {
			return fmt.Errorf("classify nonstandard flag parsing on command %q before adding public error probes", name)
		}
		if len(command.MutuallyExclusiveFlags) != 0 {
			return fmt.Errorf("classify mutually exclusive flags on command %q and add group-specific error probes", name)
		}
		entry := errorCoverageCommand{Path: slices.Clone(path), Aliases: slices.Clone(command.Aliases), Compatibility: compatibility, Class: class, ActionContract: actionContract}
		for _, child := range command.Commands {
			marked, _ := child.Metadata["command-compatibility-alias"].(bool)
			if !child.Hidden || marked {
				entry.HasChildren = true
			}
		}
		if entry.HasChildren && (command.Action != nil || len(command.Arguments) != 0 || command.ArgValidator != nil) {
			switch name {
			case "help", "help setup", "tokenizer":
				entry.Class += "; reviewed positional-error group"
				entry.ActionContract = name
			default:
				return fmt.Errorf("classify action-bearing group %q; unknown-child probes must not execute valid positional actions", name)
			}
		}
		for _, flag := range command.Flags {
			names := flag.Names()
			if len(names) == 0 || names[0] == "" {
				return fmt.Errorf("classify unnamed flag on command %q", name)
			}
			declared := errorCoverageFlag{Name: names[0], Aliases: slices.Clone(names[1:]), Owner: name, Origin: "application declaration"}
			if visible, ok := flag.(cli.VisibleFlag); ok {
				declared.Hidden = !visible.IsVisible()
			}
			if declared.Hidden {
				reason, exists := hiddenFlags[name+"/--"+names[0]]
				if !exists {
					return fmt.Errorf("classify hidden flag --%s on command %q and its privacy contract", names[0], name)
				}
				manifest.Exclusions[name+"/--"+names[0]+" display"] = reason
			}
			if documented, ok := flag.(cli.DocGenerationFlag); ok {
				declared.Type, declared.TakesValue = documented.TypeName(), documented.TakesValue()
			} else {
				return fmt.Errorf("classify undocumented flag --%s on command %q", names[0], name)
			}
			var classified bool
			declared.Policy, classified = errorCoverageTypePolicy(declared.Type)
			if !classified {
				return fmt.Errorf("classify type %q for --%s on command %q; add an invalid-value probe or an explicit syntax policy", declared.Type, names[0], name)
			}
			if local, ok := flag.(cli.LocalFlag); ok {
				declared.Persistent = !local.IsLocal()
			}
			if required, ok := flag.(cli.RequiredFlag); ok {
				declared.Required = required.IsRequired()
			}
			if required, ok := flag.(requestflag.RequiredFlagOrStdin); ok {
				declared.RequiredAfterInput = !declared.Required && required.IsRequiredAsFlagOrStdin()
			}
			entry.Flags = append(entry.Flags, declared)
		}
		framework, err := errorCoverageFrameworkDeclarations(command, name, len(path) == 0, hideHelp, entry.Flags)
		if err != nil {
			return err
		}
		entry.Flags = append(entry.Flags, framework...)
		entry.Effective = slices.Clone(entry.Flags)
		localNames := map[string]bool{}
		for _, flag := range entry.Flags {
			for _, alias := range append([]string{flag.Name}, flag.Aliases...) {
				localNames[alias] = true
			}
		}
		inheritedNames := map[string]string{}
		// urfave applies nearest ancestors first and suppresses a persistent
		// declaration when any current local name shadows one of its names.
		for index := len(ancestors) - 1; index >= 0; index-- {
			for _, flag := range ancestors[index] {
				if !flag.Persistent {
					continue
				}
				names := append([]string{flag.Name}, flag.Aliases...)
				if slices.ContainsFunc(names, func(name string) bool { return localNames[name] }) {
					entry.Shadowed = append(entry.Shadowed, flag.Owner+"/--"+flag.Name)
					continue
				}
				key := flag.Owner + "/--" + flag.Name
				for _, alias := range names {
					if previous, exists := inheritedNames[alias]; exists && previous != key {
						return fmt.Errorf("classify overlapping inherited alias %q on command %q: %s and %s", alias, name, previous, key)
					}
					inheritedNames[alias] = key
				}
				entry.Effective = append(entry.Effective, flag)
				group, exists := groups[key]
				if !exists {
					group = &errorCoverageGroup{Key: key, Flag: flag}
					groups[key] = group
				}
				group.Scopes = append(group.Scopes, slices.Clone(path))
				if len(path) > len(group.Representative) {
					group.Representative = slices.Clone(path)
				}
			}
		}
		entry.EffectiveNames = map[string]string{}
		for _, flag := range entry.Effective {
			for _, alias := range append([]string{flag.Name}, flag.Aliases...) {
				if _, exists := entry.EffectiveNames[alias]; !exists {
					entry.EffectiveNames[alias] = flag.Owner + "/--" + flag.Name
				}
			}
		}
		manifest.Commands = append(manifest.Commands, entry)
		for _, child := range command.Commands {
			if err := visit(child, append(slices.Clone(path), child.Name), compatibility, append(slices.Clone(ancestors), entry.Flags), hideHelp); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root, nil, false, nil, false); err != nil {
		return manifest, err
	}
	for _, group := range groups {
		manifest.Groups = append(manifest.Groups, *group)
	}
	sort.Slice(manifest.Groups, func(i, j int) bool { return manifest.Groups[i].Key < manifest.Groups[j].Key })
	return manifest, nil
}

// Only this fresh helper configures the production tree. The pure builder
// inventories it without initializing parser state or invoking actions.
func TestMainErrorCoverageManifestProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_ERROR_COVERAGE_MANIFEST") != "1" {
		return
	}
	root := commands.Command
	root.Flags = append(root.Flags, custom.NewRequestHeaderFlag())
	if _, _, err := custom.ConfigureHelp(root, []string{"openai", "--help"}); err != nil {
		t.Fatal(err)
	}
	custom.ConfigureCommandErrors(root)
	manifest, err := buildErrorCoverageManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func errorCoverageEnvironment(home, baseURL string) []string {
	var environment []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if !strings.HasPrefix(name, "OPENAI_") && !slices.Contains([]string{"COMPLETION_STYLE", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "ZDOTDIR"}, name) {
			environment = append(environment, entry)
		}
	}
	return append(environment, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home,
		"APPDATA="+home, "ZDOTDIR="+home,
		"OPENAI_API_KEY=synthetic-private-key", "OPENAI_ADMIN_KEY=synthetic-private-admin-key",
		"OPENAI_BASE_URL="+baseURL, "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb", "CI=1")
}

func readErrorCoverageManifest(t *testing.T, environment []string) errorCoverageManifest {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainErrorCoverageManifestProcess$")
	child.Env = append(slices.Clone(environment), "OPENAI_CLI_ERROR_COVERAGE_MANIFEST=1")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		t.Fatalf("read production command declarations: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("declaration helper wrote stderr: %q", stderr.String())
	}
	var manifest errorCoverageManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode production declarations: %v", err)
	}
	if len(manifest.Commands) == 0 {
		t.Fatal("production command declaration audit selected no commands")
	}
	return manifest
}

type errorCoverageCase struct {
	Name, Kind string
	Args       []string
	Code       int
	Message    string
	Contracts  []string
}

// Representative contracts come from declarations, never diagnostic prose.
// Tags cover independent parser dimensions without crossing every command family.
func errorCoverageContracts(kind string, command errorCoverageCommand, spelling string, flags ...errorCoverageFlag) []string {
	scope := "root"
	if len(command.Path) > 0 {
		scope = "canonical"
		if command.Compatibility {
			scope = "compatibility"
		}
		if command.HasChildren {
			scope += "-group"
		} else {
			scope += "-leaf"
		}
	}
	tags := []string{"kind:" + kind, "scope:" + scope}
	if kind == "required-options" {
		framework, input := false, false
		for _, flag := range command.Flags {
			framework = framework || flag.Required
			input = input || flag.RequiredAfterInput
		}
		stage := "input"
		if framework {
			stage = "framework"
			if input {
				stage = "mixed"
			}
		}
		tags = append(tags, "required-stage:"+stage)
	}
	if len(command.Path) > 0 && strings.Contains(command.Path[0], ":") {
		tags = append(tags, "path-form:colon")
	}
	if command.ActionContract != "" {
		tags = append(tags, "action:"+command.ActionContract+":"+kind)
	}
	for _, flag := range flags {
		tags = append(tags, "type:"+kind+":"+flag.Type, "origin:"+kind+":"+flag.Origin)
		if spelling != flag.Name {
			tags = append(tags, "alias:"+spelling)
		} else if len(spelling) == 1 {
			tags = append(tags, "canonical-spelling:short")
		} else {
			tags = append(tags, "canonical-spelling:long")
		}
		if flag.Hidden {
			tags = append(tags, "hidden:"+kind+":"+flag.Type)
		}
		if strings.Contains(flag.Name, ".") {
			tags = append(tags, "nested-option:"+kind+":"+flag.Type)
		}
	}
	return tags
}

func errorCoverageFlagSpelling(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

func errorCoverageTypeDescription(name string) string {
	switch name {
	case "int", "uint":
		return "an integer, such as 1"
	case "float":
		return "a number, such as 0.5"
	case "bool", "boolean":
		return "a boolean (true or false)"
	case "duration":
		return "a duration, such as 1s"
	// No production date declaration exists today. A future declaration must
	// expose expected syntax rather than silently accepting missing guidance.
	case "date":
		return "a date, such as 2026-01-01"
	case "datetime":
		return "a date and time, such as 2026-01-01T00:00:00Z"
	case "time":
		return "a time, such as 12:00:00"
	default:
		return ""
	}
}

func declaredErrorCoverageCases(manifest errorCoverageManifest) []errorCoverageCase {
	var cases []errorCoverageCase
	const private = "synthetic-private-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	rootVersion, builtinScope := false, map[string]bool{}
	commands := map[string]errorCoverageCommand{}
	for _, command := range manifest.Commands {
		commands[strings.Join(command.Path, " ")] = command
		if len(command.Path) == 0 {
			rootVersion = slices.ContainsFunc(command.Flags, func(flag errorCoverageFlag) bool { return flag.Origin == "urfave version" })
		}
	}
	for _, command := range manifest.Commands {
		path := strings.Join(command.Path, " ")
		invocation := strings.TrimSpace("openai " + path)
		help := "\nOptions and examples: " + invocation + " --help"
		add := func(kind, flag string, suffix []string, code int, message string, declarations ...errorCoverageFlag) {
			cases = append(cases, errorCoverageCase{
				Name: strings.TrimSpace(path + "/" + kind + "/" + flag), Kind: kind,
				Args: append(slices.Clone(command.Path), suffix...), Code: code, Message: message,
				Contracts: errorCoverageContracts(kind, command, flag, declarations...),
			})
		}
		add("unknown-option", "", []string{"--" + private + "=synthetic-private-value\x1b[31m"}, 1, "An option is not recognized."+help)
		if rootVersion && len(command.Path) != 0 && command.EffectiveNames["version"] == "" {
			group := "nested"
			if strings.Contains(command.Path[0], ":") {
				group = "compatibility"
			}
			if !builtinScope[group] && !command.HasChildren {
				add("framework-version-scope", "version", []string{"--version=synthetic-private-value"}, 1,
					"The --version option is not available for this command."+help)
				builtinScope[group] = true
			}
		}
		for _, alias := range command.Aliases {
			if len(command.Path) == 0 {
				continue
			}
			aliasPath := append(slices.Clone(command.Path[:len(command.Path)-1]), alias)
			cases = append(cases, errorCoverageCase{Name: strings.Join(aliasPath, " ") + "/command-alias", Kind: "command-alias",
				Args: append(aliasPath, "--"+private), Code: 1, Message: "An option is not recognized." + help,
				Contracts: append(errorCoverageContracts("command-alias", command, ""), "command-alias:"+alias)})
		}
		if command.HasChildren {
			message := "Unknown command. Run openai help"
			if path != "" {
				message += " " + path
			}
			message += " to see commands."
			code := 3
			switch path {
			case "help":
				message = "Unknown help topic. Run openai help to see commands."
			case "help setup":
				message = "Setup help takes no additional arguments."
			case "tokenizer":
				// This action-bearing group owns positional input validation.
				message = "Tokenizer takes no positional input. Open the editor without arguments, or use tokenizer count --text TEXT."
				code = 1
			}
			add("unknown-child", "", []string{private}, code, message)
		}
		var required, requiredAfterInput []string
		aliasOwners := map[string]string{}
		for _, flag := range command.Flags {
			for _, alias := range append([]string{flag.Name}, flag.Aliases...) {
				if _, exists := aliasOwners[alias]; !exists {
					aliasOwners[alias] = flag.Name
				}
			}
		}
		for _, flag := range command.Flags {
			name := errorCoverageFlagSpelling(flag.Name)
			typeDescription := errorCoverageTypeDescription(flag.Type)
			if flag.TakesValue {
				message := "Add a value for " + name + "." + help
				add("missing-value", flag.Name, []string{name}, 1, message, flag)
			}
			if typeDescription != "" {
				message := "Invalid value for " + name + ". Expected " + typeDescription + "." + help
				if flag.Hidden {
					message = "Invalid value for " + name + "." + help
				}
				add("invalid-type", flag.Name, []string{name + "=synthetic-private-value\x1b[31m"}, 1, message, flag)
			}
			for _, alias := range flag.Aliases {
				if aliasOwners[alias] != flag.Name {
					continue
				} // The parser uses the first declaration for shared aliases.
				if flag.TakesValue {
					message := "Add a value for " + name + "." + help
					add("flag-alias-missing-value", alias, []string{errorCoverageFlagSpelling(alias)}, 1, message, flag)
				} else if typeDescription != "" {
					message := "Invalid value for " + name + ". Expected " + typeDescription + "." + help
					if flag.Hidden {
						message = "Invalid value for " + name + "." + help
					}
					add("flag-alias-invalid-type", alias, []string{errorCoverageFlagSpelling(alias) + "=synthetic-private-value"}, 1, message, flag)
				}
			}
			if flag.Required {
				required = append(required, name)
			}
			if flag.RequiredAfterInput {
				requiredAfterInput = append(requiredAfterInput, name)
			}
		}
		// The framework rejects its missing flags before the action can check
		// request flags supplied through stdin or positional input.
		if len(required) == 0 {
			required = requiredAfterInput
		}
		if len(required) != 0 {
			add("required-options", "", nil, 1, "Missing required options: "+strings.Join(required, ", ")+"."+help)
		}
	}
	// Every inherited scope maps to its declaring owner. Execute each inherited
	// declaration and spelling at its deepest representative, not at every leaf.
	for _, group := range manifest.Groups {
		flag := group.Flag
		help := "\nOptions and examples: openai " + strings.Join(group.Representative, " ") + " --help"
		for _, spelling := range append([]string{flag.Name}, flag.Aliases...) {
			suffix, message := errorCoverageFlagSpelling(spelling), ""
			if flag.TakesValue {
				message = "Add a value for " + errorCoverageFlagSpelling(flag.Name) + "." + help
			} else if expected := errorCoverageTypeDescription(flag.Type); expected != "" {
				suffix += "=synthetic-private-value"
				message = "Invalid value for " + errorCoverageFlagSpelling(flag.Name) + ". Expected " + expected + "." + help
			}
			if message == "" {
				continue
			}
			if flag.Hidden && !flag.TakesValue {
				message = "Invalid value for " + errorCoverageFlagSpelling(flag.Name) + "." + help
			}
			cases = append(cases, errorCoverageCase{Name: strings.Join(group.Representative, " ") + "/inherited-equivalence/" + spelling,
				Kind: "inherited-equivalence", Args: append(slices.Clone(group.Representative), suffix), Code: 1, Message: message,
				Contracts: errorCoverageContracts("inherited-equivalence", commands[strings.Join(group.Representative, " ")], spelling, flag)})
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases
}

type errorCoverageSelection struct {
	Mode          string
	Indices       []int
	Start, End    int
	ShardIndex    int
	ShardCount    int
	ContractCount int
}

func selectErrorCoverageCases(cases []errorCoverageCase, lookup func(string) (string, bool)) (errorCoverageSelection, error) {
	selection := errorCoverageSelection{Mode: "representative", End: len(cases)}
	if len(cases) == 0 {
		return selection, fmt.Errorf("coverage inventory contains no cases")
	}
	contracts := map[string]bool{}
	for index, test := range cases {
		if len(test.Contracts) == 0 {
			return selection, fmt.Errorf("classify representative contracts for case %d (%s)", index, test.Name)
		}
		for _, contract := range test.Contracts {
			if contract == "" {
				return selection, fmt.Errorf("classify empty representative contract for case %d", index)
			}
			contracts[contract] = true
		}
	}
	selection.ContractCount = len(contracts)
	const prefix = "OPENAI_CLI_ERROR_COVERAGE_"
	mode, modeSet := lookup(prefix + "MODE")
	if modeSet && mode != "representative" && mode != "exhaustive" {
		return selection, fmt.Errorf("%sMODE must be representative or exhaustive", prefix)
	}
	start, startSet := lookup(prefix + "START")
	end, endSet := lookup(prefix + "END")
	shardIndex, indexSet := lookup(prefix + "SHARD_INDEX")
	shardCount, countSet := lookup(prefix + "SHARD_COUNT")
	ranged, sharded := startSet || endSet, indexSet || countSet
	if ranged && sharded || modeSet && mode == "representative" && (ranged || sharded) {
		return selection, fmt.Errorf("representative mode, explicit ranges, and shards cannot be combined")
	}
	parse := func(name, raw string) (int, error) {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("%s%s must be an integer: %w", prefix, name, err)
		}
		return value, nil
	}
	var err error
	switch {
	case ranged:
		selection.Mode = "range"
		if startSet {
			selection.Start, err = parse("START", start)
			if err != nil {
				return selection, err
			}
		}
		if endSet {
			selection.End, err = parse("END", end)
			if err != nil {
				return selection, err
			}
		}
	case sharded:
		if !indexSet || !countSet {
			return selection, fmt.Errorf("SHARD_INDEX and SHARD_COUNT must both be set")
		}
		selection.Mode = "shard"
		selection.ShardIndex, err = parse("SHARD_INDEX", shardIndex)
		if err != nil {
			return selection, err
		}
		selection.ShardCount, err = parse("SHARD_COUNT", shardCount)
		if err != nil {
			return selection, err
		}
		if selection.ShardCount <= 0 || selection.ShardCount > len(cases) || selection.ShardIndex < 0 || selection.ShardIndex >= selection.ShardCount {
			return selection, fmt.Errorf("require 0 <= SHARD_INDEX < SHARD_COUNT <= case count (%d); empty shards are invalid", len(cases))
		}
		width, extra := len(cases)/selection.ShardCount, len(cases)%selection.ShardCount
		selection.Start = selection.ShardIndex*width + min(selection.ShardIndex, extra)
		selection.End = selection.Start + width
		if selection.ShardIndex < extra {
			selection.End++
		}
	case mode == "exhaustive":
		selection.Mode = "exhaustive"
	default:
		seen := map[string]bool{}
		for index, test := range cases {
			needed := false
			for _, contract := range test.Contracts {
				if !seen[contract] {
					needed, seen[contract] = true, true
				}
			}
			if needed {
				selection.Indices = append(selection.Indices, index)
			}
		}
		return selection, nil
	}
	if selection.Start < 0 || selection.End > len(cases) || selection.Start >= selection.End {
		return selection, fmt.Errorf("invalid or empty coverage range [%d,%d) for %d cases", selection.Start, selection.End, len(cases))
	}
	for index := selection.Start; index < selection.End; index++ {
		selection.Indices = append(selection.Indices, index)
	}
	return selection, nil
}

func errorCoverageInventoryFingerprint(manifest errorCoverageManifest, cases []errorCoverageCase) (string, error) {
	data, err := json.Marshal(struct {
		Manifest errorCoverageManifest
		Cases    []errorCoverageCase
	}{manifest, cases})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

type errorCoverageWorker struct {
	environment []string
	server      *httptest.Server
	requests    atomic.Int32
}

func newErrorCoverageWorker(t *testing.T, aggregate *atomic.Int32) *errorCoverageWorker {
	t.Helper()
	worker := &errorCoverageWorker{}
	worker.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		worker.requests.Add(1)
		aggregate.Add(1)
		w.WriteHeader(http.StatusTeapot) // Do not trigger automatic HTTP retries.
	}))
	t.Cleanup(worker.server.Close)
	worker.environment = errorCoverageEnvironment(t.TempDir(), worker.server.URL)
	return worker
}

// Inventory every declaration on every run. Default execution covers representative
// contracts; explicit exhaustive mode or shards execute the complete case matrix.
// Each selected error uses a fresh public main process and JSON diagnostics.
func TestMainErrorCoverageAcrossCommandTree(t *testing.T) {
	var requests atomic.Int32
	workers := [2]*errorCoverageWorker{
		newErrorCoverageWorker(t, &requests),
		newErrorCoverageWorker(t, &requests),
	}
	manifest := readErrorCoverageManifest(t, workers[0].environment)
	cases := declaredErrorCoverageCases(manifest)
	selection, err := selectErrorCoverageCases(cases, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := errorCoverageInventoryFingerprint(manifest, cases)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"mode": selection.Mode, "total": len(cases), "selected_count": len(selection.Indices),
		"inventory_sha256": fingerprint, "representative_contract_count": selection.ContractCount}
	if selection.Mode != "representative" {
		metadata["start"], metadata["end"] = selection.Start, selection.End
	}
	if selection.Mode == "shard" {
		metadata["shard_index"], metadata["shard_count"] = selection.ShardIndex, selection.ShardCount
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("error-coverage-metadata: %s", encoded)
	counts := map[string]int{}
	types := map[string]int{}
	compatibility := 0
	for _, command := range manifest.Commands {
		for _, flag := range command.Flags {
			types[flag.Type]++
		}
		if command.Compatibility {
			compatibility++
		}
	}
	for _, test := range cases {
		counts[test.Kind]++
	}
	if selection.Mode == "representative" {
		t.Logf("production coverage: commands=%d compatibility=%d categories=%v selected=representatives count=%d total=%d exclusions=%v",
			len(manifest.Commands), compatibility, counts, len(selection.Indices), len(cases), manifest.Exclusions)
	} else {
		t.Logf("production coverage: commands=%d compatibility=%d categories=%v selected=[%d,%d) total=%d exclusions=%v",
			len(manifest.Commands), compatibility, counts, selection.Start, selection.End, len(cases), manifest.Exclusions)
	}
	t.Logf("declared types=%v; free-form string/any/map values have no universal invalid scalar; endpoint validation remains server-owned", types)
	for _, group := range manifest.Groups {
		t.Logf("inherited equivalence: owner=%q aliases=%v type=%q scopes=%d representative=%q", group.Key, group.Flag.Aliases, group.Flag.Type, len(group.Scopes), group.Representative)
	}
	effective, shadowed := 0, 0
	for _, command := range manifest.Commands {
		effective += len(command.Effective)
		shadowed += len(command.Shadowed)
	}
	t.Logf("effective inventory: flags=%d shadowed=%d inherited-groups=%d; every scope maps to an owner and representative", effective, shadowed, len(manifest.Groups))
	t.Log("Inherited scopes map to executable equivalence groups. Synthetic guards verify shadowing. Literal and -- boundaries use focused recovery and native-shell tests.")
	indices := make(chan int)
	var running sync.WaitGroup
	for _, worker := range workers {
		running.Add(1)
		go func(worker *errorCoverageWorker) {
			defer running.Done()
			for index := range indices {
				test := cases[index]
				t.Run(fmt.Sprintf("%05d/%s", index, test.Name), func(t *testing.T) {
					before := worker.requests.Load()
					args := append([]string{"openai", "--format-error", "json"}, test.Args...)
					got := runMainDispatchWithEnv(t, "bash", worker.environment, args...)
					if got.code != test.Code || got.stdout != "" {
						t.Fatalf("public error changed status or streams: %+v; want status %d", got, test.Code)
					}
					if worker.requests.Load() != before {
						t.Fatal("local diagnostic made an HTTP request")
					}
					if strings.Contains(got.stderr, "synthetic-private") || strings.ContainsAny(got.stderr, "\x1b\a\r") {
						t.Fatalf("diagnostic exposed rejected input: %q", got.stderr)
					}
					var payload map[string]any
					if err := json.Unmarshal([]byte(got.stderr), &payload); err != nil {
						t.Fatalf("entire stderr is not one JSON error: %v; %q", err, got.stderr)
					}
					if len(payload) != 1 || payload["message"] != test.Message {
						t.Fatalf("incomplete declared recovery: got %#v; want %q", payload, test.Message)
					}
				})
			}
		}(worker)
	}
	for _, index := range selection.Indices {
		indices <- index
	}
	close(indices)
	// Concurrent T.Run calls must finish before the parent checks requests or
	// returns. Do not use t.Parallel, which would defer children past this check.
	running.Wait()
	if requests.Load() != 0 {
		t.Fatalf("declaration audit made %d HTTP requests", requests.Load())
	}
}

func TestErrorCoverageWorkerIsolation(t *testing.T) {
	var aggregate atomic.Int32
	workers := [2]*errorCoverageWorker{
		newErrorCoverageWorker(t, &aggregate),
		newErrorCoverageWorker(t, &aggregate),
	}
	var environments [2]map[string]string
	for index, worker := range workers {
		values := map[string]string{}
		for _, entry := range worker.environment {
			name, value, _ := strings.Cut(entry, "=")
			values[strings.ToUpper(name)] = value
		}
		home := values["HOME"]
		if home == "" || values["OPENAI_BASE_URL"] != worker.server.URL {
			t.Fatalf("worker %d lost its isolated home or endpoint", index)
		}
		for _, name := range []string{"USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "ZDOTDIR"} {
			if values[name] != home {
				t.Fatalf("worker %d: %s escaped the isolated home", index, name)
			}
		}
		environments[index] = values
	}
	if environments[0]["HOME"] == environments[1]["HOME"] {
		t.Fatal("workers share their home or configuration directory")
	}
	const marker = "synthetic-worker-state"
	if err := os.WriteFile(filepath.Join(environments[0]["XDG_CONFIG_HOME"], marker), []byte("worker one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(environments[1]["XDG_CONFIG_HOME"], marker)); !os.IsNotExist(err) {
		t.Fatalf("worker state was not isolated: %v", err)
	}
	request := func(index int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, environments[index]["OPENAI_BASE_URL"], nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := workers[index].server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusTeapot {
			t.Fatalf("unexpected fixture status: %d", response.StatusCode)
		}
	}
	before := workers[0].requests.Load()
	request(1)
	if workers[0].requests.Load() != before || workers[1].requests.Load() != 1 || aggregate.Load() != 1 {
		t.Fatal("another worker's request changed local attribution or aggregate accounting")
	}
	request(0)
	request(0)
	if workers[0].requests.Load() != 2 || workers[1].requests.Load() != 1 || aggregate.Load() != 3 {
		t.Fatal("repeated requests lost worker isolation or aggregate accounting")
	}
}

func TestErrorCoverageRepresentativeSelection(t *testing.T) {
	manifest := errorCoverageManifest{Commands: []errorCoverageCommand{
		{},
		{Path: []string{"first"}, Flags: []errorCoverageFlag{{Name: "limit", Aliases: []string{"l"}, Type: "int", TakesValue: true, Origin: "application declaration"}}},
		{Path: []string{"second"}, Flags: []errorCoverageFlag{{Name: "limit", Aliases: []string{"l"}, Type: "int", TakesValue: true, Origin: "application declaration"}}},
		{Path: []string{"timed"}, Flags: []errorCoverageFlag{{Name: "interval", Type: "duration", TakesValue: true, Origin: "application declaration"}}},
		{Path: []string{"group:legacy"}, Compatibility: true, Flags: []errorCoverageFlag{{Name: "settings.ratio", Type: "float", TakesValue: true, Origin: "application declaration"}}},
		{Path: []string{"help"}, HasChildren: true, ActionContract: "help", Flags: []errorCoverageFlag{{Name: "all", Type: "bool", Hidden: true, Origin: "application declaration"}}},
		{Path: []string{"help", "setup"}, HasChildren: true, ActionContract: "help setup"},
	}}
	cases := declaredErrorCoverageCases(manifest)
	lookup := func(string) (string, bool) { return "", false }
	selection, err := selectErrorCoverageCases(cases, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Mode != "representative" || len(selection.Indices) == 0 || len(selection.Indices) >= len(cases) {
		t.Fatalf("default did not select a bounded representative set: %+v", selection)
	}
	all, selected := map[string]bool{}, map[string]bool{}
	for _, test := range cases {
		for _, contract := range test.Contracts {
			all[contract] = true
		}
	}
	for _, index := range selection.Indices {
		for _, contract := range cases[index].Contracts {
			selected[contract] = true
		}
	}
	for contract := range all {
		if !selected[contract] {
			t.Errorf("representatives omit contract %q", contract)
		}
	}
	for _, contract := range []string{"kind:unknown-option", "scope:root", "scope:canonical-leaf", "scope:compatibility-leaf", "alias:l",
		"type:invalid-type:int", "type:invalid-type:float", "type:invalid-type:bool", "type:invalid-type:duration", "nested-option:invalid-type:float",
		"hidden:invalid-type:bool", "action:help:unknown-child", "action:help setup:unknown-child", "path-form:colon"} {
		if !selected[contract] {
			t.Errorf("declaration metadata did not produce contract %q", contract)
		}
	}
	changed := slices.Clone(cases)
	for index := range changed {
		changed[index].Message = "Unrelated wording, with no type or command information."
	}
	again, err := selectErrorCoverageCases(changed, lookup)
	if err != nil || !slices.Equal(selection.Indices, again.Indices) {
		t.Fatalf("message prose changed deterministic selection: %+v, %v", again, err)
	}
	before, err := errorCoverageInventoryFingerprint(manifest, cases)
	if err != nil {
		t.Fatal(err)
	}
	after, err := errorCoverageInventoryFingerprint(manifest, changed)
	if err != nil || before == after {
		t.Fatalf("complete-inventory fingerprint lost expected messages: %q %q %v", before, after, err)
	}
	manifest.Commands = append(manifest.Commands, errorCoverageCommand{Path: []string{"future"},
		Flags: []errorCoverageFlag{{Name: "enabled", Type: "boolean", Origin: "application declaration"}}})
	future := declaredErrorCoverageCases(manifest)
	full, err := selectErrorCoverageCases(future, func(name string) (string, bool) {
		return "exhaustive", name == "OPENAI_CLI_ERROR_COVERAGE_MODE"
	})
	if err != nil || len(future) <= len(cases) || len(full.Indices) != len(future) {
		t.Fatalf("new declarations did not enter exhaustive coverage: %+v %v", full, err)
	}
	if _, err := selectErrorCoverageCases([]errorCoverageCase{{Name: "unclassified"}}, lookup); err == nil {
		t.Fatal("a future case without representative metadata bypassed classification")
	}
}

func TestErrorCoverageSelectionControls(t *testing.T) {
	cases := make([]errorCoverageCase, 13)
	for index := range cases {
		cases[index] = errorCoverageCase{Name: strconv.Itoa(index), Contracts: []string{"one reviewed contract"}}
	}
	for _, tc := range []struct {
		name       string
		variables  map[string]string
		mode       string
		start, end int
	}{
		{"full", map[string]string{"MODE": "exhaustive"}, "exhaustive", 0, 13},
		{"legacy start", map[string]string{"START": "3"}, "range", 3, 13},
		{"legacy end", map[string]string{"END": "5"}, "range", 0, 5},
		{"legacy range", map[string]string{"START": "3", "END": "8"}, "range", 3, 8},
		{"explicit range", map[string]string{"MODE": "exhaustive", "START": "3", "END": "8"}, "range", 3, 8},
		{"first shard", map[string]string{"MODE": "exhaustive", "SHARD_INDEX": "0", "SHARD_COUNT": "4"}, "shard", 0, 4},
		{"implicit shard", map[string]string{"SHARD_INDEX": "3", "SHARD_COUNT": "4"}, "shard", 10, 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection, err := selectErrorCoverageCases(cases, func(name string) (string, bool) {
				value, found := tc.variables[strings.TrimPrefix(name, "OPENAI_CLI_ERROR_COVERAGE_")]
				return value, found
			})
			if err != nil || selection.Mode != tc.mode || selection.Start != tc.start || selection.End != tc.end || len(selection.Indices) != tc.end-tc.start {
				t.Fatalf("unexpected selection: %+v, %v", selection, err)
			}
		})
	}
	for _, variables := range []map[string]string{
		{"MODE": ""}, {"MODE": "full"}, {"START": ""}, {"END": "bad"}, {"START": "-1"}, {"END": "14"},
		{"START": "3", "END": "3"}, {"START": "5", "END": "4"}, {"SHARD_INDEX": "0"}, {"SHARD_COUNT": "4"},
		{"SHARD_INDEX": "", "SHARD_COUNT": "4"}, {"SHARD_INDEX": "0", "SHARD_COUNT": "bad"},
		{"SHARD_INDEX": "-1", "SHARD_COUNT": "4"}, {"SHARD_INDEX": "4", "SHARD_COUNT": "4"},
		{"SHARD_INDEX": "0", "SHARD_COUNT": "0"}, {"SHARD_INDEX": "0", "SHARD_COUNT": "14"},
		{"START": "0", "SHARD_INDEX": "0", "SHARD_COUNT": "4"},
		{"MODE": "representative", "END": "4"}, {"MODE": "representative", "SHARD_INDEX": "0", "SHARD_COUNT": "4"},
	} {
		t.Run(fmt.Sprint(variables), func(t *testing.T) {
			if _, err := selectErrorCoverageCases(cases, func(name string) (string, bool) {
				value, found := variables[strings.TrimPrefix(name, "OPENAI_CLI_ERROR_COVERAGE_")]
				return value, found
			}); err == nil {
				t.Fatalf("invalid or conflicting selection was accepted: %v", variables)
			}
		})
	}
	if _, err := selectErrorCoverageCases(nil, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("empty inventory was accepted")
	}
}

func TestErrorCoverageShardUnion(t *testing.T) {
	for _, total := range []int{4, 7, 13, 16, 17} {
		cases := make([]errorCoverageCase, total)
		for index := range cases {
			cases[index] = errorCoverageCase{Name: strconv.Itoa(index), Contracts: []string{"reviewed contract"}}
		}
		seen, next, smallest, largest := map[int]bool{}, 0, total, 0
		for shard := 0; shard < 4; shard++ {
			selection, err := selectErrorCoverageCases(cases, func(name string) (string, bool) {
				switch name {
				case "OPENAI_CLI_ERROR_COVERAGE_MODE":
					return "exhaustive", true
				case "OPENAI_CLI_ERROR_COVERAGE_SHARD_INDEX":
					return strconv.Itoa(shard), true
				case "OPENAI_CLI_ERROR_COVERAGE_SHARD_COUNT":
					return "4", true
				default:
					return "", false
				}
			})
			if err != nil || selection.Start != next {
				t.Fatalf("non-contiguous shard for total %d: %+v, %v", total, selection, err)
			}
			for _, index := range selection.Indices {
				if seen[index] {
					t.Fatalf("duplicate case %d for total %d", index, total)
				}
				seen[index] = true
			}
			next = selection.End
			smallest, largest = min(smallest, len(selection.Indices)), max(largest, len(selection.Indices))
		}
		if next != total || len(seen) != total || largest-smallest > 1 {
			t.Fatalf("shards lost coverage or balance: total=%d covered=%d min=%d max=%d", total, len(seen), smallest, largest)
		}
	}
}

type unclassifiedErrorCoverageFlag struct{ cli.StringFlag }

func (*unclassifiedErrorCoverageFlag) TypeName() string { return "future-unclassified-type" }

func TestErrorCoverageRejectsUnclassifiedDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name string
		root *cli.Command
		want string
	}{
		{"new type", &cli.Command{Name: "openai", Flags: []cli.Flag{&unclassifiedErrorCoverageFlag{cli.StringFlag{Name: "future-flag"}}}}, "classify type \"future-unclassified-type\" for --future-flag"},
		{"new hidden route", &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "future-hidden", Hidden: true}}}, "classify hidden command \"future-hidden\""},
		{"new hidden flag", &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "future-hidden", Hidden: true}}}, "classify hidden flag --future-hidden"},
		{"new parser contract", &cli.Command{Name: "openai", SkipFlagParsing: true}, "classify nonstandard flag parsing"},
		{"new external flags", &cli.Command{Name: "openai", AllowExtFlags: true}, "classify external runtime flags"},
		{"new help collision", &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "host", Aliases: []string{"h"}}}}, "classify framework help collision"},
		{"new flag group", &cli.Command{Name: "openai", MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{{Flags: [][]cli.Flag{{&cli.StringFlag{Name: "future-grouped-flag"}}}}}}, "classify mutually exclusive flags"},
		{"new action group", &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "future-group", Commands: []*cli.Command{{Name: "child"}}, Action: func(context.Context, *cli.Command) error { t.Fatal("the inventory executed an action"); return nil }}}}, "classify action-bearing group \"future-group\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildErrorCoverageManifest(tc.root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unclassified declaration did not require coverage: got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestErrorCoverageInventoriesInheritanceAndAliasShadowing(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "project", Aliases: []string{"p"}}, &cli.BoolFlag{Name: "quiet"}}, Commands: []*cli.Command{
		{Name: "shadowed", Flags: []cli.Flag{&cli.IntFlag{Name: "count", Aliases: []string{"p"}, Local: true}}},
		{Name: "group", Commands: []*cli.Command{{Name: "leaf", Aliases: []string{"ls"}, Flags: []cli.Flag{&requestflag.Flag[string]{Name: "input"}}}}},
	}}
	manifest, err := buildErrorCoverageManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range manifest.Commands {
		switch strings.Join(command.Path, " ") {
		case "shadowed":
			if !slices.Contains(command.Shadowed, "/--project") {
				t.Fatal("shadowed owner absent from manifest")
			}
			if command.EffectiveNames["p"] != "shadowed/--count" {
				t.Fatal("effective alias did not select the child declaration")
			}
			if slices.ContainsFunc(command.Effective, func(flag errorCoverageFlag) bool { return flag.Name == "project" }) {
				t.Fatal("shadowed project remained effective")
			}
		case "group leaf":
			if !slices.Contains(command.Aliases, "ls") {
				t.Fatal("command alias absent from manifest")
			}
			if !slices.ContainsFunc(command.Effective, func(flag errorCoverageFlag) bool {
				return flag.Name == "project" && flag.Owner == "" && slices.Contains(flag.Aliases, "p")
			}) {
				t.Fatal("inherited flag identity absent from manifest")
			}
		}
	}
	if len(manifest.Groups) != 2 {
		t.Fatalf("got %d inherited owner groups; want 2", len(manifest.Groups))
	}
	for _, group := range manifest.Groups {
		if strings.Join(group.Representative, " ") != "group leaf" {
			t.Fatalf("representative lost deepest effective scope: %+v", group)
		}
	}
	cases := declaredErrorCoverageCases(manifest)
	if !slices.ContainsFunc(cases, func(test errorCoverageCase) bool {
		return test.Kind == "inherited-equivalence" && slices.Equal(test.Args, []string{"group", "leaf", "-p"})
	}) {
		t.Fatal("inherited alias has no executable representative")
	}
	if root.Flags[0].IsSet() || root.Commands[0].Flags[0].IsSet() {
		t.Fatal("inventory changed parser state")
	}
}

func TestErrorCoverageModelsFrameworkFlags(t *testing.T) {
	t.Run("required validation stages", func(t *testing.T) {
		framework := func() cli.Flag { return &cli.StringFlag{Name: "output", Required: true} }
		input := func() cli.Flag {
			return &requestflag.Flag[string]{Name: "resource-id", PathParam: "resource_id", Required: true}
		}
		root := &cli.Command{Name: "openai", Commands: []*cli.Command{
			{Name: "framework", Flags: []cli.Flag{framework()}},
			{Name: "input", Flags: []cli.Flag{input()}},
			{Name: "mixed", Flags: []cli.Flag{input(), framework()}},
		}}
		manifest, err := buildErrorCoverageManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		cases := declaredErrorCoverageCases(manifest)
		selection, err := selectErrorCoverageCases(cases, func(string) (string, bool) { return "", false })
		if err != nil {
			t.Fatal(err)
		}
		for _, stage := range []string{"framework", "input", "mixed"} {
			index := slices.IndexFunc(cases, func(test errorCoverageCase) bool {
				return test.Name == stage+"/required-options/"
			})
			if index < 0 || !slices.Contains(selection.Indices, index) {
				t.Fatalf("required stage %s lost its executable representative", stage)
			}
			flag := "--output"
			if stage == "input" {
				flag = "--resource-id"
			}
			want := "Missing required options: " + flag + ".\nOptions and examples: openai " + stage + " --help"
			if cases[index].Message != want || !slices.Contains(cases[index].Contracts, "required-stage:"+stage) {
				t.Fatalf("required stage %s: %+v; want %q", stage, cases[index], want)
			}
		}
		mixed := manifest.Commands[len(manifest.Commands)-1]
		if len(mixed.Flags) < 2 || mixed.Flags[0].Required || !mixed.Flags[0].RequiredAfterInput ||
			!mixed.Flags[1].Required || mixed.Flags[1].RequiredAfterInput {
			t.Fatalf("mixed declarations lost their independent validation stages: %+v", mixed.Flags)
		}
	})
	root := &cli.Command{Name: "openai", Version: "synthetic", Flags: []cli.Flag{&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}}},
		Commands: []*cli.Command{{Name: "group", Commands: []*cli.Command{{Name: "leaf"}}},
			{Name: "group:legacy", Hidden: true, Metadata: map[string]any{"command-compatibility-alias": true}, Commands: []*cli.Command{{Name: "leaf"}}},
			{Name: "no-help", HideHelp: true, Commands: []*cli.Command{{Name: "leaf"}}}}}
	manifest, err := buildErrorCoverageManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range manifest.Commands {
		var help, version *errorCoverageFlag
		for index := range command.Flags {
			flag := &command.Flags[index]
			if flag.Origin == "urfave help" {
				help = flag
			}
			if flag.Origin == "urfave version" {
				version = flag
			}
		}
		hideHelp := len(command.Path) > 0 && command.Path[0] == "no-help"
		if (help == nil) != hideHelp {
			t.Fatalf("wrong inherited help policy: %+v", command)
		}
		if help != nil && (help.Persistent || help.TakesValue || !slices.Equal(help.Aliases, []string{"h"})) {
			t.Fatalf("wrong framework help shape: %+v", help)
		}
		if len(command.Path) == 0 {
			if version == nil || version.Persistent || len(version.Aliases) != 0 || command.EffectiveNames["v"] != "/--verbose" {
				t.Fatalf("version alias collision was not preserved: %+v", command)
			}
		} else if version != nil || command.EffectiveNames["version"] != "" {
			t.Fatalf("local version leaked into nested scope: %+v", command)
		}
	}
	for _, root := range []*cli.Command{{Name: "openai"}, {Name: "openai", Version: "synthetic", HideVersion: true}} {
		got, err := buildErrorCoverageManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(got.Commands[0].Flags, func(flag errorCoverageFlag) bool { return flag.Origin == "urfave version" }) {
			t.Fatal("disabled version entered the inventory")
		}
	}
	cases := declaredErrorCoverageCases(manifest)
	for _, path := range [][]string{nil, {"group", "leaf"}, {"group:legacy", "leaf"}} {
		if !slices.ContainsFunc(cases, func(test errorCoverageCase) bool {
			return test.Kind == "invalid-type" && slices.Equal(test.Args, append(slices.Clone(path), "--help=synthetic-private-value\x1b[31m"))
		}) {
			t.Fatalf("framework help has no public invalid-bool probe: %q", path)
		}
	}
	if len(root.Flags) != 1 || len(root.Commands[0].Flags) != 0 {
		t.Fatal("framework accounting mutated declarations")
	}
	for _, flag := range []cli.Flag{&cli.StringFlag{Name: "help", Aliases: []string{"h"}}, &cli.BoolFlag{Name: "help", Aliases: []string{"h"}}, &cli.BoolFlag{Name: "help", Aliases: []string{"x"}, Local: true}} {
		if _, err := errorCoverageFrameworkFlag(flag, "help", "h", ""); err == nil {
			t.Fatal("unsupported framework flag change bypassed the guard")
		}
	}
}
