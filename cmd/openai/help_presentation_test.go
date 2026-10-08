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
	_, section, found := strings.Cut(text, start+":\n")
	if !found {
		return ""
	}
	section, _, _ = strings.Cut(section, "\n"+end+":")
	return strings.TrimSpace(section)
}

func TestMainHelpPresentationPreservesDetailsAfterShortName(t *testing.T) {
	got := runMainDispatch(t, "bash", "openai", "responses", "create", "--help")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("help failed: %+v", got)
	}
	name := helpSection(got.stdout, "NAME", "SYNOPSIS")
	if strings.Contains(name, "https://") || !strings.Contains(name, "Creates a model response.") {
		t.Fatalf("NAME is not a short command summary: %s", name)
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

func TestMainHelpPresentationConcreteSyntaxAndLabels(t *testing.T) {
	for _, tc := range []struct {
		path []string
		want []string
	}{
		{[]string{"models", "retrieve"}, []string{"[MODEL | --model MODEL]", "Model ID.", "JSON/YAML keys: model."}},
		{[]string{"files", "upload"}, []string{"[--file PATH]", "[--purpose TEXT]", "Required request inputs: --file, --purpose."}},
		{[]string{"images", "generate"}, []string{"--size SIZE", "--quality QUALITY", "--background BACKGROUND", "--output-format FORMAT", "--inline MODE"}},
	} {
		got := runMainDispatch(t, "bash", append(append([]string{"openai"}, tc.path...), "--help")...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("help failed: %+v", got)
		}
		plain := strings.Join(strings.Fields(got.stdout), " ")
		for _, want := range tc.want {
			if !strings.Contains(plain, want) {
				t.Errorf("%q omitted %q: %s", tc.path, want, got.stdout)
			}
		}
		if strings.Contains(helpSection(got.stdout, "SYNOPSIS", "DESCRIPTION"), "[OPTIONS]") {
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
		_, globals, found := strings.Cut(got.stdout, "GLOBAL OPTIONS:\n")
		if !found || !strings.Contains(globals, "Global option details: openai --help") {
			t.Fatalf("global option reference missing: %s", got.stdout)
		}
		for _, name := range []string{"--format", "--format-error", "--transform", "--raw-output", "-r", "--header", "-H", "--base-url"} {
			if !strings.Contains(globals, name) {
				t.Errorf("global option index lost %s", name)
			}
		}
		if strings.Contains(globals, "Choose how results are displayed") || strings.Contains(globals, "- json: full JSON") {
			t.Error("global index repeated full or inapplicable format descriptions")
		}
		if path[0] == "images" {
			flat := strings.Join(strings.Fields(globals), " ")
			for _, note := range []string{"--format auto or text only", "cannot use --transform or --raw-output"} {
				if !strings.Contains(flat, note) {
					t.Errorf("local restriction missing beside global index: %s", note)
				}
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
