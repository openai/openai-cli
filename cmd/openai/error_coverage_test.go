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
}

type errorCoverageCommand struct {
	Path          []string
	Aliases       []string
	Compatibility bool
	HasChildren   bool
	Flags         []errorCoverageFlag
}

type errorCoverageManifest struct {
	Commands   []errorCoverageCommand
	Exclusions map[string]string
}

// Only this fresh helper mutates the production command tree. It never runs a
// command action or initializes parser state shared with another test.
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
	manifest := errorCoverageManifest{Exclusions: map[string]string{}}
	internalContracts := map[string]string{
		"@completion":         "Setup failures use TestMainRecoveryCompletionSetup; shell protocols use native completion tests.",
		"__complete":          "Uses completion protocol statuses and deliberately disables ordinary flag parsing.",
		"@manpages":           "Destination failures, partial files, and recovery use TestMainRecoveryManpageOutput.",
		"tokenizer __preview": "Runs the isolated tokenizer worker protocol, not an ordinary public command.",
		"tokenizer __output":  "Runs the isolated terminal output protocol, not an ordinary public command.",
	}
	var visit func(*cli.Command, []string, bool)
	visit = func(command *cli.Command, path []string, compatibility bool) {
		marked, _ := command.Metadata["command-compatibility-alias"].(bool)
		compatibility = compatibility || marked
		if command.Hidden && !marked {
			name := strings.Join(path, " ")
			reason, exists := internalContracts[name]
			if !exists {
				t.Fatalf("classify the new hidden command's contract: %q", name)
			}
			manifest.Exclusions[name] = reason
			return
		}
		if command.SkipFlagParsing {
			t.Fatalf("classify public command with disabled flag parsing: %q", path)
		}
		entry := errorCoverageCommand{Path: slices.Clone(path), Aliases: slices.Clone(command.Aliases), Compatibility: compatibility}
		for _, child := range command.Commands {
			marked, _ := child.Metadata["command-compatibility-alias"].(bool)
			if !child.Hidden || marked {
				entry.HasChildren = true
			}
		}
		for _, flag := range command.Flags {
			if visible, ok := flag.(cli.VisibleFlag); ok && !visible.IsVisible() {
				continue
			}
			names := flag.Names()
			if len(names) == 0 {
				t.Fatalf("unnamed flag on %q", path)
			}
			declared := errorCoverageFlag{Name: names[0], Aliases: slices.Clone(names[1:])}
			if documented, ok := flag.(cli.DocGenerationFlag); ok {
				declared.Type, declared.TakesValue = documented.TypeName(), documented.TakesValue()
			} else {
				t.Fatalf("classify undocumented flag %q on %q", names[0], path)
			}
			if required, ok := flag.(cli.RequiredFlag); ok {
				declared.Required = required.IsRequired()
			}
			if required, ok := flag.(requestflag.RequiredFlagOrStdin); ok {
				declared.Required = declared.Required || required.IsRequiredAsFlagOrStdin()
			}
			entry.Flags = append(entry.Flags, declared)
		}
		manifest.Commands = append(manifest.Commands, entry)
		for _, child := range command.Commands {
			visit(child, append(slices.Clone(path), child.Name), compatibility)
		}
	}
	visit(root, nil, false)
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
		for _, flag := range command.Flags {
			name := "--" + flag.Name
			if len(flag.Name) == 1 {
				name = "-" + flag.Name
			}
			if flag.TakesValue {
				add("missing-value", flag.Name, []string{name}, 1, "Add a value for "+name+"."+help)
			}
			typeDescription := ""
			switch flag.Type {
			case "int", "uint":
				typeDescription = "an integer, such as 1"
			case "float":
				typeDescription = "a number, such as 0.5"
			case "bool", "boolean":
				typeDescription = "a boolean (true or false)"
			}
			if typeDescription != "" {
				add("invalid-type", flag.Name, []string{name + "=synthetic-private-value\x1b[31m"}, 1,
					"Invalid value for "+name+". Expected "+typeDescription+"."+help)
			}
			if slices.Contains([]string{"date", "datetime", "time"}, flag.Type) {
				add("invalid-type", flag.Name, []string{name + "=synthetic-private-invalid-date"}, 1,
					"Invalid value for "+name+"."+help)
			}
			for _, alias := range flag.Aliases {
				if !flag.TakesValue {
					continue
				}
				prefix := "--"
				if len(alias) == 1 {
					prefix = "-"
				}
				add("flag-alias-missing-value", alias, []string{prefix + alias}, 1, "Add a value for "+name+"."+help)
			}
			if flag.Required {
				required = append(required, name)
			}
		}
		if len(required) != 0 {
			add("required-options", "", nil, 1, "Missing required options: "+strings.Join(required, ", ")+"."+help)
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
	t.Log("Inherited and shadowed options use TestParserErrorSuggestionsRespectScope, TestParserErrorTypeUsesChildFlag, and TestStdinValueErrorUsesEffectiveCommandScope. Literal and -- boundaries use TestMainRecovery and native-shell tests.")
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
