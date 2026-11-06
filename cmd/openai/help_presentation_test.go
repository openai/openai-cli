package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func helpSection(text, start, end string) string {
	_, section, found := strings.Cut(text, start+"\n")
	if !found {
		return ""
	}
	section, _, _ = strings.Cut(section, "\n"+end+"\n")
	return strings.TrimSpace(section)
}

func TestMainFullHelpLinksToTheRightKeySetup(t *testing.T) {
	for _, tc := range []struct {
		path, topic string
	}{
		{"admin", "help setup admin"},
		{"admin roles update", "help setup admin"},
		{"admin:organization:projects list", "help setup admin"},
		{"projects list", "help setup admin"},
		{"models retrieve", "help setup"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			argv := append([]string{"openai", "help", "--all"}, strings.Fields(tc.path)...)
			got := runMainDispatch(t, "bash", argv...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Key setup: openai "+tc.topic+"\n") {
				t.Fatalf("expected %q key setup in %s: %+v", tc.topic, tc.path, got)
			}
		})
	}
}

func TestMainUploadOverviewExplainsFileRetention(t *testing.T) {
	for _, path := range []string{"files upload", "files create", "uploads create"} {
		t.Run(path, func(t *testing.T) {
			argv := append([]string{"openai"}, strings.Fields(path)...)
			got := runMainDispatch(t, "bash", append(argv, "--help")...)
			text := strings.Join(strings.Fields(got.stdout), " ")
			const retention = "Unless you set --expires-after, batch files expire after 30 days and other files stay until deleted."
			if got.code != 0 || got.stderr != "" || strings.Count(text, retention) != 1 {
				t.Fatalf("file retention missing or duplicated on %s: %+v", path, got)
			}
			if path == "uploads create" && !strings.Contains(text, "expires after an hour") {
				t.Fatalf("intermediate upload lifetime missing: %s", text)
			}
		})
	}
}

func TestMainSpeechHelpDistinguishesPrivateFormAndDirectRequests(t *testing.T) {
	for _, path := range [][]string{{"audio", "speak"}, {"speak"}} {
		got := runMainDispatch(t, "bash", append(append([]string{"openai"}, path...), "--help")...)
		text := strings.Join(strings.Fields(got.stdout), " ")
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("%v help failed: %+v", path, got)
		}
		for _, want := range []string{
			"Run without command flags in a terminal to open speech controls.",
			"The form retains audio, text, and instructions in private local history.",
			"For direct output, supply --input, --model, and --voice through flags or piped JSON/YAML.",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%v lost %q: %s", path, want, got.stdout)
			}
		}
		if strings.Contains(text, "Required request inputs:") {
			t.Errorf("%v incorrectly marks interactive form inputs as required", path)
		}
	}
}

func TestMainReferenceSizedHelpWithFewFlags(t *testing.T) {
	short := runMainDispatch(t, "bash", "openai", "models", "list", "--help")
	full := runMainDispatch(t, "bash", "openai", "help", "--all", "models", "list")
	if short.code != 0 || full.code != 0 || short.stderr != "" || full.stderr != "" {
		t.Fatalf("help failed: %v / %v", short, full)
	}
	for _, want := range []string{"$ openai models list", "--filter", "--sort-by", "--max-items", "q quits", "openai help --all models list"} {
		if !strings.Contains(short.stdout, want) {
			t.Errorf("brief models list help omitted %q: %s", want, short.stdout)
		}
	}
	const grammar = "Filter escapes are decoded before RE2."
	if strings.Contains(strings.Join(strings.Fields(short.stdout), " "), grammar) ||
		!strings.Contains(strings.Join(strings.Fields(full.stdout), " "), grammar) {
		t.Fatal("the detailed filter grammar belongs in the full reference")
	}
}

func TestMainCompactHelpExplainsRequiredInputs(t *testing.T) {
	for _, path := range [][]string{{"audio", "transcribe"}, {"admin", "usage", "audio-speeches"}} {
		got := runMainDispatch(t, "bash", append(append([]string{"openai"}, path...), "--help")...)
		flat := strings.Join(strings.Fields(got.stdout), " ")
		if got.code != 0 || got.stderr != "" || !strings.Contains(flat, "Required request inputs:") ||
			!strings.Contains(flat, "Supply them through flags or piped JSON/YAML.") {
			t.Fatalf("%s help lost required input guidance: %s", strings.Join(path, " "), got.stdout)
		}
	}
}

func TestMainHelpForOneFlag(t *testing.T) {
	full := runMainDispatch(t, "bash", "openai", "help", "--all", "responses", "create")
	if full.code != 0 || full.stderr != "" {
		t.Fatalf("full help failed: %+v", full)
	}
	for _, tc := range []struct{ help, flag, want string }{
		{"-h", "--model", "Model ID."},
		{"--help", "--stream", "Default: false"},
		{"-h", "--input", "Text, image, or file inputs"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			got := runMainDispatch(t, "bash", "openai", "responses", "create", tc.help, tc.flag)
			if got.code != 0 || got.stderr != "" || !strings.HasPrefix(strings.TrimSpace(got.stdout), tc.flag+"=") {
				t.Fatalf("single flag help failed: %+v", got)
			}
			if !strings.Contains(got.stdout, tc.want) || !strings.Contains(full.stdout, got.stdout) {
				t.Errorf("single flag help differs from the full entry: %q", got.stdout)
			}
		})
	}
	got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_API_KEY=fake-private-env-flag-help"}, nil,
		"openai", "--api-key", "fake-private-argument-flag-help", "responses", "create", "-h", "--api-key")
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "OPENAI_API_KEY") ||
		strings.Contains(got.stdout, "fake-private-") || strings.Contains(got.stdout, "SYNOPSIS") {
		t.Errorf("inherited credential help failed or showed a configured value")
	}
	header := runMainDispatch(t, "bash", "openai", "responses", "create", "-h", "-H")
	if header.code != 0 || header.stderr != "" || !strings.HasPrefix(strings.TrimSpace(header.stdout), "--header=") ||
		!strings.Contains(header.stdout, "-H=") || strings.Contains(header.stdout, "SYNOPSIS") {
		t.Errorf("inherited alias lookup failed: %+v", header)
	}
}

func TestMainVersionAndCompletionFlagHelp(t *testing.T) {
	for _, spelling := range []string{"--version", "-v"} {
		got := runMainDispatch(t, "bash", "openai", "-h", spelling)
		if got.code != 0 || got.stderr != "" || strings.Join(strings.Fields(got.stdout), " ") != "--version, -v print the version" {
			t.Errorf("lookup for %s did not return its flag entry: %+v", spelling, got)
		}
	}
	got := runMainDispatch(t, "bash", "openai", "--version")
	if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "openai version ") {
		t.Errorf("version action changed: %+v", got)
	}
	profile := runMainDispatch(t, "bash", "openai", "@completion", "-h", "--profile")
	if profile.code != 0 || profile.stderr != "" || !strings.HasPrefix(strings.TrimSpace(profile.stdout), "--profile=") ||
		!strings.Contains(profile.stdout, "startup file") || strings.Contains(profile.stdout, "Shell completion") {
		t.Errorf("completion profile lookup did not return its flag entry: %+v", profile)
	}
	unrelated := runMainDispatch(t, "bash", "openai", "@completion", "-h", "--api-key")
	if unrelated.code != 0 || unrelated.stderr != "" || !strings.HasPrefix(unrelated.stdout, "Shell completion") {
		t.Errorf("inherited request flag replaced the completion guide: %+v", unrelated)
	}
}

func TestMainHelpDocumentsScriptingRestrictions(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{"batches browse --help", "Requires terminal stdin, stdout, and stderr. Use batches list in scripts."},
		{"models list --help", "Filtering and sorting do not support --format raw."},
		{"models list -h --filter", "Cannot be used with --format raw."},
		{"models list -h --sort-by", "Cannot be used with --format raw."},
	} {
		got := runMainDispatch(t, "bash", append([]string{"openai"}, strings.Fields(tc.args)...)...)
		if got.code != 0 || got.stderr != "" || !strings.Contains(strings.Join(strings.Fields(got.stdout), " "), tc.want) {
			t.Errorf("%s omitted its scripting restriction: %+v", tc.args, got)
		}
	}
}

func TestMainHelpForOneFlagUsesLocalOutputGuidance(t *testing.T) {
	for _, tc := range []struct {
		path       []string
		flag, want string
	}{
		{[]string{"explore"}, "--format", "Search supports auto or text for --format and --format-error."},
		{[]string{"explore"}, "--format-error", "Search supports auto or text for --format and --format-error."},
		{[]string{"images", "preview"}, "--format", "supports --format auto or text only"},
		{[]string{"images", "preview"}, "-r", "cannot use --transform or --raw-output"},
		{[]string{"explore"}, "--transform-error", "Search does not support --transform-error."},
		{[]string{"explore"}, "-r", "Search does not support --raw-output."},
		{[]string{"tokenizer", "view"}, "--raw-output", "does not support --transform or --raw-output"},
	} {
		args := append([]string{"openai"}, tc.path...)
		got := runMainDispatch(t, "bash", append(args, "-h", tc.flag)...)
		if got.code != 0 || got.stderr != "" || !strings.Contains(strings.Join(strings.Fields(got.stdout), " "), tc.want) ||
			strings.Contains(got.stdout, "json") || strings.Contains(got.stdout, "SYNOPSIS") {
			t.Errorf("scoped flag help for %v %s: %+v", tc.path, tc.flag, got)
		}
	}
	// License output uses the standard formatter even though its overview explains text extraction.
	licenses := runMainDispatch(t, "bash", "openai", "audio", "licenses", "-h", "--format")
	if licenses.code != 0 || licenses.stderr != "" || !strings.HasPrefix(strings.TrimSpace(licenses.stdout), "--format=") ||
		!strings.Contains(licenses.stdout, "json: full JSON") || strings.Contains(licenses.stdout, "FULL REFERENCE") {
		t.Fatalf("audio licenses omitted the supported format reference: %+v", licenses)
	}
	for _, path := range [][]string{{"audio", "licenses"}, {"audio", "play"}, {"audio", "export"}, {"images", "preview"}} {
		for _, flag := range []string{"--format-error", "--transform-error"} {
			args := append(append([]string{"openai"}, path...), "-h", flag)
			got := runMainDispatch(t, "bash", args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, flag) || strings.Contains(got.stdout, "FULL REFERENCE") ||
				strings.Contains(got.stdout, "openai "+strings.Join(path, " ")) {
				t.Errorf("scoped %v %s did not show the applicable error option: %+v", path, flag, got)
			}
		}
	}
	// --format is not the --format-error mentioned in the playback guidance.
	for _, tc := range []struct {
		path []string
		flag string
	}{
		{[]string{"audio", "play"}, "--format"},
		{[]string{"explore"}, "--api-key"},
	} {
		args := append([]string{"openai"}, tc.path...)
		want := runMainDispatch(t, "bash", append(args, "--help")...)
		got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-private-scoped-help"}, append(args, "-h", tc.flag)...)
		if got.code != 0 || got.stderr != "" || want.code != 0 || got.stdout != want.stdout ||
			strings.Contains(got.stdout, "synthetic-private-scoped-help") {
			t.Errorf("scoped help for %v %s did not fall back to its ordinary page", tc.path, tc.flag)
		}
	}
}

func TestMainActionHelpPreservesConsequencesAndPreconditions(t *testing.T) {
	for _, tc := range []struct{ path, detail string }{
		{"conversations delete", "Items in the conversation will not be deleted."},
		{"vector-stores files delete", "the file itself will not be deleted. To delete the file, use the delete file endpoint."},
		{"admin external-storage delete", "Repeating a deletion also completes any interrupted retention update. Cloud storage is unchanged."},
		{"batches cancel", "it will have partial results (if any) available in the output file."},
		{"responses cancel", "Only responses created with the background parameter set to true can be cancelled."},
		{"admin projects archive", "Archived projects cannot be used or updated."},
		{"projects archive", "Archived projects cannot be used or updated."},
		{"admin projects create", "Projects can be created and archived, but cannot be deleted."},
		{"projects create", "Projects can be created and archived, but cannot be deleted."},
		{"admin projects service-accounts create", "this also returns an unredacted API key for the service account."},
		{"admin invites delete", "If the invite has already been accepted, it cannot be deleted."},
	} {
		t.Run(tc.path, func(t *testing.T) {
			args := append([]string{"openai"}, strings.Fields(tc.path)...)
			got := runMainDispatch(t, "bash", append(args, "--help")...)
			summary, _, _ := strings.Cut(got.stdout, "\n\n")
			plain := strings.Join(strings.Fields(summary), " ")
			if got.code != 0 || got.stderr != "" || !strings.Contains(plain, tc.detail) || strings.Contains(plain, "https://") || strings.Contains(plain, " Learn more.") {
				t.Fatalf("action overview lost its documented meaning: %+v", got)
			}
		})
	}
}

func TestMainHelpPresentationPreservesDetailsAfterShortName(t *testing.T) {
	got := runMainDispatch(t, "bash", "openai", "responses", "create", "--help")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("help failed: %+v", got)
	}
	overview, _, _ := strings.Cut(got.stdout, "\nEXAMPLE\n")
	if !strings.Contains(overview, "openai responses create [FLAGS]") || strings.Count(overview, "Creates a model response") != 1 {
		t.Fatalf("missing usage or one-line purpose: %s", overview)
	}
	for _, absent := range []string{"https://", "NAME\n", "SYNOPSIS\n", "DESCRIPTION\n", "web search", "--access-programs=", "echo '"} {
		if strings.Contains(got.stdout, absent) {
			t.Errorf("ordinary help included %q", absent)
		}
	}
	rows := helpSection(got.stdout, "COMMON FLAGS", "ALL FLAGS")
	for _, name := range []string{"--model", "--input", "--instructions", "--stream", "--max-output-tokens"} {
		found := false
		for _, line := range strings.Split(rows, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 && fields[0] == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no explained row for %s in %s", name, rows)
		}
	}
	got = runMainDispatch(t, "bash", "openai", "help", "--all", "responses", "create")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("full reference failed: %+v", got)
	}
	description := helpSection(got.stdout, "DESCRIPTION", "EXAMPLES")
	for _, want := range []string{"guides/text", "guides/images-vision", "guides/structured-outputs", "guides/function-calling", "guides/tools", "guides/tools-web-search", "guides/tools-file-search"} {
		if !strings.Contains(description, want) {
			t.Errorf("DESCRIPTION lost reference %q", want)
		}
	}
	if strings.Contains(description, "](") {
		t.Errorf("DESCRIPTION retained unrendered Markdown: %s", description)
	}
	for _, label := range []string{"Built-in tools", "MCP Tools", "Function calls (custom tools)"} {
		if !strings.Contains(got.stdout, label) || strings.Contains(got.stdout, "**"+label+"**") {
			t.Errorf("tool guidance lost its text or retained emphasis markers: %s", label)
		}
	}
	plain := strings.Join(strings.Fields(got.stdout), " ")
	if !strings.Contains(plain, "'flex' (https://developers.openai.com/api/docs/guides/flex-processing)") {
		t.Error("quoted service-tier value must retain its reference outside the quotes")
	}
}

func TestMainBriefFlagDescriptionsFollowCommandAliases(t *testing.T) {
	for _, tc := range []struct{ route, flag, brief, detail string }{
		{"chat completions create", "--stream", "Stream the reply as it is generated.", "server-sent events"},
		{"chat:completions create", "--stream", "Stream the reply as it is generated.", "server-sent events"},
		{"beta responses create", "--input", "Text, image, or file input.", "inputs to the model, used to generate a response."},
		{"beta:responses create", "--input", "Text, image, or file input.", "inputs to the model, used to generate a response."},
	} {
		t.Run(tc.route, func(t *testing.T) {
			path := append([]string{"openai"}, strings.Fields(tc.route)...)
			short := runMainDispatch(t, "bash", append(path, "--help")...)
			if short.code != 0 || short.stderr != "" || !strings.Contains(strings.Join(strings.Fields(short.stdout), " "), tc.flag+" "+tc.brief) {
				t.Fatalf("ordinary help lost the flag overview: %+v", short)
			}
			for _, args := range [][]string{
				append(path, "-h", tc.flag),
				append([]string{"openai", "help", "--all"}, strings.Fields(tc.route)...),
			} {
				got := runMainDispatch(t, "bash", args...)
				if got.code != 0 || got.stderr != "" || !strings.Contains(strings.Join(strings.Fields(got.stdout), " "), tc.detail) {
					t.Fatalf("%q lost the complete flag description: %+v", args, got)
				}
			}
		})
	}
}

func TestMainHelpPresentationConcreteSyntaxAndLabels(t *testing.T) {
	for _, tc := range []struct {
		path []string
		want []string
	}{
		{[]string{"models", "retrieve"}, []string{"[MODEL | --model=MODEL]", "Model ID.", "JSON/YAML keys: model."}},
		{[]string{"files", "upload"}, []string{"[PATH | --file PATH] --purpose PURPOSE [options]", "Use PATH or --file PATH and set --purpose explicitly.", "A leading @ is part of the filename.", "Piped JSON/YAML accepts file and purpose."}},
		{[]string{"files", "create"}, []string{"[--file=PATH]", "[--purpose=TEXT]", "Required request inputs: --file, --purpose."}},
		{[]string{"images", "generate"}, []string{"--size=SIZE", "--quality=QUALITY", "--background=BACKGROUND", "--output-format=FORMAT", "--inline=MODE"}},
	} {
		args := append(append([]string{"openai"}, tc.path...), "--help")
		if tc.path[0] == "images" {
			args = append([]string{"openai", "help", "--all"}, tc.path...)
		}
		got := runMainDispatch(t, "bash", args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("help failed: %+v", got)
		}
		plain := strings.Join(strings.Fields(got.stdout), " ")
		for _, want := range tc.want {
			if !strings.Contains(plain, want) {
				t.Errorf("%q omitted %q: %s", tc.path, want, got.stdout)
			}
		}
		if strings.Contains(got.stdout, "[OPTIONS]") {
			t.Errorf("%q retained a generic synopsis", tc.path)
		}
	}
}

func TestMainHelpPresentationCompactGlobalsAndLocalRestrictions(t *testing.T) {
	root := runMainDispatch(t, "bash", "openai", "--help")
	for _, path := range [][]string{{"models", "list"}, {"images", "preview"}, {"images", "inline", "on"}} {
		got := runMainDispatch(t, "bash", append(append([]string{"openai"}, path...), "--help")...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("help failed: %+v", got)
		}
		// The inline toggle has no additional command-specific options.
		if path[1] != "inline" && !strings.Contains(got.stdout, "openai help --all "+strings.Join(path, " ")) {
			t.Fatalf("full command reference missing: %s", got.stdout)
		}
		for _, unrelated := range []string{"--header", "--base-url", "Key setup:", "Choose how results are displayed"} {
			if strings.Contains(got.stdout, unrelated) {
				t.Errorf("ordinary help for %v included %q", path, unrelated)
			}
		}
		if path[0] == "images" {
			flat := strings.Join(strings.Fields(got.stdout), " ")
			for _, note := range []string{"--format auto or text only", "cannot use --transform or --raw-output"} {
				if !strings.Contains(flat, note) {
					t.Errorf("local restriction missing: %s", note)
				}
			}
		}
		full := runMainDispatch(t, "bash", append([]string{"openai", "help", "--all"}, path...)...)
		if full.code != 0 || full.stderr != "" {
			t.Fatalf("full reference failed for %v: %+v", path, full)
		}
		_, globals, _ := strings.Cut(full.stdout, "GLOBAL FLAGS\n")
		for _, name := range []string{"--format", "--format-error", "--transform", "--raw-output", "-r", "--header", "-H", "--base-url"} {
			if !strings.Contains(globals, name) {
				t.Errorf("full global index lost %s", name)
			}
		}
	}
	if root.code != 0 || root.stderr != "" || !strings.Contains(root.stdout, "Choose how results are displayed") || !strings.Contains(root.stdout, "Env: OPENAI_API_KEY") {
		t.Fatalf("root lost global option details: %+v", root)
	}
}

func TestMainHelpPresentationDocumentedInputSourcesExecute(t *testing.T) {
	requests := make(chan string, 12)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic", "object": "model", "created": 0, "owned_by": "synthetic"})
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, stdin, path string
		args              []string
	}{
		{"model positional", "", "/models/model-synthetic", []string{"models", "retrieve", "model-synthetic"}},
		{"model flag", "", "/models/model-synthetic", []string{"models", "retrieve", "--model", "model-synthetic"}},
		{"model JSON", `{"model":"model-synthetic"}`, "/models/model-synthetic", []string{"models", "retrieve"}},
		{"model YAML", "model: model-synthetic\n", "/models/model-synthetic", []string{"models", "retrieve"}},
		{"multiple positionals", "", "/vector_stores/vs-synthetic/files/file-synthetic", []string{"vector-stores", "files", "retrieve", "vs-synthetic", "file-synthetic"}},
		{"mixed sources", `{"file_id":"file-synthetic"}`, "/vector_stores/vs-synthetic/files/file-synthetic", []string{"vector-stores", "files", "retrieve", "--vector-store-id", "vs-synthetic"}},
		{"multiple YAML keys", "vector_store_id: vs-synthetic\nfile_id: file-synthetic\n", "/vector_stores/vs-synthetic/files/file-synthetic", []string{"vector-stores", "files", "retrieve"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.txt")
			if err := os.WriteFile(path, []byte(tc.stdin), 0600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			args := append([]string{"openai", "--format", "json"}, tc.args...)
			got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_API_KEY=fake-help-key", "OPENAI_BASE_URL=" + server.URL}, stdin, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, `"id": "synthetic"`) {
				t.Fatalf("documented syntax failed: %+v", got)
			}
			select {
			case request := <-requests:
				if request != "GET "+tc.path {
					t.Errorf("input sources changed request target: %s", request)
				}
			default:
				t.Fatal("documented syntax did not reach the synthetic request")
			}
		})
	}
}
