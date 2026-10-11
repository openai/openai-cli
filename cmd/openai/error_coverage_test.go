package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/requestflag"
	apicmd "github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

type errorCoverageFlag struct {
	Name       string
	Aliases    []string
	Type       string
	TakesValue bool
	Required   bool
	Owner      string
	Persistent bool
	Hidden     bool
	Policy     string
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
	case "int", "uint", "float", "bool", "boolean", "date", "datetime", "time":
		return "typed invalid value", true
	case "string", "any", "":
		return "no universally invalid scalar; missing values and request decoding cover syntax", true
	case "string=any":
		return "object fields accept arbitrary values; missing values and request decoding cover syntax", true
	default:
		return "", false
	}
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
	var visit func(*cli.Command, []string, bool, [][]errorCoverageFlag) error
	visit = func(command *cli.Command, path []string, compatibility bool, ancestors [][]errorCoverageFlag) error {
		name := strings.Join(path, " ")
		marked, _ := command.Metadata["command-compatibility-alias"].(bool)
		compatibility = compatibility || marked
		class := "public"
		if command.Hidden && !marked {
			if reason, exists := internalContracts[name]; exists {
				manifest.Exclusions[name] = reason
				return nil
			}
			if reason, exists := hiddenUtilities[name]; exists {
				class = "hidden utility: " + reason
			} else {
				return fmt.Errorf("classify hidden command %q and name its executable contract tests", name)
			}
		}
		if command.SkipFlagParsing || command.StopOnNthArg != nil {
			return fmt.Errorf("classify nonstandard flag parsing on command %q before adding public error probes", name)
		}
		if len(command.MutuallyExclusiveFlags) != 0 {
			return fmt.Errorf("classify mutually exclusive flags on command %q and add group-specific error probes", name)
		}
		entry := errorCoverageCommand{Path: slices.Clone(path), Aliases: slices.Clone(command.Aliases), Compatibility: compatibility, Class: class}
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
			default:
				return fmt.Errorf("classify action-bearing group %q; unknown-child probes must not execute valid positional actions", name)
			}
		}
		for _, flag := range command.Flags {
			names := flag.Names()
			if len(names) == 0 || names[0] == "" {
				return fmt.Errorf("classify unnamed flag on command %q", name)
			}
			declared := errorCoverageFlag{Name: names[0], Aliases: slices.Clone(names[1:]), Owner: name}
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
				declared.Required = declared.Required || required.IsRequiredAsFlagOrStdin()
			}
			entry.Flags = append(entry.Flags, declared)
		}
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
			if err := visit(child, append(slices.Clone(path), child.Name), compatibility, append(slices.Clone(ancestors), entry.Flags)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root, nil, false, nil); err != nil {
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
	root := apicmd.Command
	root.Flags = append(root.Flags, apicmd.NewRequestHeaderFlag())
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
		t.Fatalf("read production command declarations: %v; stderr=%q", err, stderr.String())
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
	for _, command := range manifest.Commands {
		path := strings.Join(command.Path, " ")
		invocation := strings.TrimSpace("openai " + path)
		help := "\nOptions and examples: " + invocation + " --help"
		add := func(kind, flag string, suffix []string, code int, message string) {
			cases = append(cases, errorCoverageCase{
				Name: strings.TrimSpace(path + "/" + kind + "/" + flag), Kind: kind,
				Args: append(slices.Clone(command.Path), suffix...), Code: code, Message: message,
			})
		}
		add("unknown-option", "", []string{"--" + private + "=synthetic-private-value\x1b[31m"}, 1, "An option is not recognized."+help)
		for _, alias := range command.Aliases {
			if len(command.Path) == 0 {
				continue
			}
			aliasPath := append(slices.Clone(command.Path[:len(command.Path)-1]), alias)
			cases = append(cases, errorCoverageCase{Name: strings.Join(aliasPath, " ") + "/command-alias", Kind: "command-alias",
				Args: append(aliasPath, "--"+private), Code: 1, Message: "An option is not recognized." + help})
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
		var required []string
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
				add("missing-value", flag.Name, []string{name}, 1, message)
			}
			if typeDescription != "" {
				message := "Invalid value for " + name + ". Expected " + typeDescription + "." + help
				if flag.Hidden {
					message = "Invalid value for " + name + "." + help
				}
				add("invalid-type", flag.Name, []string{name + "=synthetic-private-value\x1b[31m"}, 1, message)
			}
			for _, alias := range flag.Aliases {
				if aliasOwners[alias] != flag.Name {
					continue
				} // The parser uses the first declaration for shared aliases.
				if flag.TakesValue {
					message := "Add a value for " + name + "." + help
					add("flag-alias-missing-value", alias, []string{errorCoverageFlagSpelling(alias)}, 1, message)
				} else if typeDescription != "" {
					message := "Invalid value for " + name + ". Expected " + typeDescription + "." + help
					if flag.Hidden {
						message = "Invalid value for " + name + "." + help
					}
					add("flag-alias-invalid-type", alias, []string{errorCoverageFlagSpelling(alias) + "=synthetic-private-value"}, 1, message)
				}
			}
			if flag.Required {
				required = append(required, name)
			}
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
				Kind: "inherited-equivalence", Args: append(slices.Clone(group.Representative), suffix), Code: 1, Message: message})
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases
}

// Cover every public declaration and intentionally retained compatibility route.
// Each error uses a fresh public main process. Focused tests cover the format
// cross-product; this audit uses JSON once per declaration to bound runtime.
func TestMainErrorCoverageAcrossCommandTree(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTeapot) // Do not trigger automatic HTTP retries.
	}))
	t.Cleanup(server.Close)
	environment := errorCoverageEnvironment(t.TempDir(), server.URL)
	manifest := readErrorCoverageManifest(t, environment)
	cases := declaredErrorCoverageCases(manifest)
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
	// Optional deterministic ranges let a coordinator divide this same complete
	// matrix across bounded validation slots. Empty selections are failures.
	start, end := 0, len(cases)
	for name, target := range map[string]*int{
		"OPENAI_CLI_ERROR_COVERAGE_START": &start,
		"OPENAI_CLI_ERROR_COVERAGE_END":   &end,
	} {
		if raw, present := os.LookupEnv(name); present {
			value, err := strconv.Atoi(raw)
			if err != nil {
				t.Fatalf("%s must be an integer: %v", name, err)
			}
			*target = value
		}
	}
	if start < 0 || end > len(cases) || start >= end {
		t.Fatalf("invalid or empty coverage range [%d,%d) for %d cases", start, end, len(cases))
	}
	t.Logf("production coverage: commands=%d compatibility=%d categories=%v selected=[%d,%d) total=%d exclusions=%v",
		len(manifest.Commands), compatibility, counts, start, end, len(cases), manifest.Exclusions)
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
	for index := start; index < end; index++ {
		test := cases[index]
		t.Run(fmt.Sprintf("%05d/%s", index, test.Name), func(t *testing.T) {
			before := requests.Load()
			args := append([]string{"openai", "--format-error", "json"}, test.Args...)
			got := runMainDispatchWithEnv(t, "bash", environment, args...)
			if got.code != test.Code || got.stdout != "" {
				t.Fatalf("public error changed status or streams: %+v; want status %d", got, test.Code)
			}
			if requests.Load() != before {
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
	if requests.Load() != 0 {
		t.Fatalf("declaration audit made %d HTTP requests", requests.Load())
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
