package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vectorStoreEmptySearch = `{"object":"vector_store.search_results.page","data":[],"search_query":["synthetic query"],"has_more":false,"next_page":null}`
const vectorStoreNoMatches = "No matches returned.\nIndexing state was not checked.\n"
const vectorStoreSearchFirst = `{"file_id":"file_first","filename":"first source.txt","score":0.25,"attributes":{"category":"guides","active":true,"revision":7},"content":[{"type":"text","text":"first source first chunk"},{"type":"text","text":"first source second chunk"}],"future_field":{"sequence":9007199254740993}}`
const vectorStoreSearchSecond = `{"file_id":"file_second","filename":"second source.txt","score":0.95,"attributes":{"category":"notes"},"content":[{"type":"text","text":"second source only chunk"}]}`
const vectorStoreSearchPage = `{"object":"vector_store.search_results.page","data":[` + vectorStoreSearchFirst + `,` + vectorStoreSearchSecond + `],"search_query":["synthetic query"],"has_more":false,"next_page":null,"future_page_field":"preserved"}`

func runVectorStoreCommand(t *testing.T, server *httptest.Server, input *os.File, args ...string) mainDispatchResult {
	t.Helper()
	home := t.TempDir()
	return runMainDispatchWithStdin(t, "bash", []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-vector-store-test", "FORCE_COLOR=0",
	}, input, append([]string{"openai"}, args...)...)
}

func vectorStoreSearchServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/vector_stores/vs_synthetic/search" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-should-retry", "false")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestMainVectorStoreSearchEmptyFormats(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		flags      []string
	}{
		{"default", vectorStoreNoMatches, nil},
		{"auto", vectorStoreNoMatches, []string{"--format", "auto"}},
		{"text", vectorStoreNoMatches, []string{"--format", "text"}},
		{"json", "", []string{"--format", "json"}},
		{"jsonl", "", []string{"--format", "jsonl"}},
		{"yaml", "", []string{"--format", "yaml"}},
		{"pretty", "", []string{"--format", "pretty"}},
		{"explore", "", []string{"--format", "explore"}},
		{"raw page", vectorStoreEmptySearch + "\n", []string{"--format", "raw"}},
		{"extraction", "", []string{"--transform", "file_id"}},
		{"raw extraction", "", []string{"--transform", "file_id", "--raw-output"}},
		{"text extraction", "No results.\n", []string{"--format", "text", "--transform", "file_id"}},
		{"raw output", "", []string{"--raw-output"}},
		{"text raw output", "No results.\n", []string{"--format", "text", "--raw-output"}},
		{"zero", "", []string{"--max-items", "0"}},
		{"unlimited", vectorStoreNoMatches, []string{"--max-items", "-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := vectorStoreSearchServer(t, http.StatusOK, vectorStoreEmptySearch)
			args := []string{"vector-stores", "search", "--vector-store-id", "vs_synthetic", "--query", "synthetic private query"}
			got := runVectorStoreCommand(t, server, nil, append(args, tc.flags...)...)
			require.Zero(t, got.code, "%+v", got)
			require.Equal(t, tc.want, got.stdout)
			if tc.name == "explore" {
				require.Equal(t, "Warning: Output format 'explore' not supported for non-terminal output; falling back to 'json'\n", got.stderr)
			} else {
				require.Empty(t, got.stderr)
			}
			require.EqualValues(t, 1, requests.Load(), "an empty search must not fetch indexing status")
			require.NotContains(t, got.stdout+got.stderr, "synthetic private query")
		})
	}
}

func TestMainVectorStoreSearchFailuresPrecedeEmptyMessage(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"unauthorized", `{"error":{"message":"synthetic denied","type":"invalid_request_error","code":"invalid_api_key"}}`, 401},
		{"forbidden", `{"error":{"message":"synthetic denied","type":"invalid_request_error","code":"permission_denied"}}`, 403},
		{"empty error", "", 502},
		{"empty object", `{}`, 502},
		{"malformed error", `{"error":`, 502},
		{"malformed success", `{"data":[`, 200},
	} {
		for _, flags := range [][]string{nil, {"--format", "json"}, {"--max-items", "0"}} {
			t.Run(tc.name+"/"+strings.Join(flags, "/"), func(t *testing.T) {
				server, requests := vectorStoreSearchServer(t, tc.status, tc.body)
				args := []string{"vector-stores", "search", "--vector-store-id", "vs_synthetic", "--query", "synthetic private query"}
				got := runVectorStoreCommand(t, server, nil, append(args, flags...)...)
				require.Equal(t, 1, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				require.NotEmpty(t, got.stderr)
				require.NotContains(t, got.stderr, "No matches")
				require.NotContains(t, got.stderr, "Indexing state")
				require.NotContains(t, got.stderr, "sk-fake-vector-store-test")
				require.NotContains(t, got.stderr, "synthetic private query")
				require.EqualValues(t, 1, requests.Load())
				if len(flags) > 0 && flags[0] == "--format" {
					require.True(t, json.Valid([]byte(got.stderr)), "structured errors must remain valid JSON: %q", got.stderr)
				}
			})
		}
	}
}

func TestMainVectorStoreSearchPreservesResults(t *testing.T) {
	for _, format := range []string{"auto", "text", "json", "jsonl", "yaml", "raw"} {
		t.Run(format, func(t *testing.T) {
			server, requests := vectorStoreSearchServer(t, http.StatusOK, vectorStoreSearchPage)
			got := runVectorStoreCommand(t, server, nil, "vector-stores", "search", "--vector-store-id", "vs_synthetic", "--query", "synthetic query", "--format", format)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.EqualValues(t, 1, requests.Load())
			for _, value := range []string{"file_first", "file_second", "first source.txt", "second source.txt", "0.25", "0.95", "guides", "notes", "true", "7", "first source first chunk", "first source second chunk", "second source only chunk", "9007199254740993"} {
				require.Contains(t, got.stdout, value)
			}
			labels := []string{"active", "revision", "future_field"}
			if format == "auto" || format == "text" {
				labels = []string{"Active: true", "Revision: 7", "Future field:"}
			}
			for _, label := range labels {
				require.Contains(t, got.stdout, label)
			}
			require.Less(t, strings.Index(got.stdout, "file_first"), strings.Index(got.stdout, "file_second"), "preserve API order even when scores increase")
			require.NotContains(t, got.stdout, "No matches")
			if format == "raw" {
				require.Equal(t, vectorStoreSearchPage+"\n", got.stdout)
			}
			if format == "json" || format == "jsonl" {
				decoder := json.NewDecoder(strings.NewReader(got.stdout))
				for _, want := range []string{vectorStoreSearchFirst, vectorStoreSearchSecond} {
					var item json.RawMessage
					require.NoError(t, decoder.Decode(&item))
					require.JSONEq(t, want, string(item))
				}
				require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
			}
		})
	}
	for _, limit := range []string{"0", "1", "-1"} {
		t.Run("extracted limit="+limit, func(t *testing.T) {
			server, requests := vectorStoreSearchServer(t, http.StatusOK, vectorStoreSearchPage)
			got := runVectorStoreCommand(t, server, nil, "vector-stores", "search", "--vector-store-id", "vs_synthetic", "--query", "synthetic query", "--transform", "file_id", "--raw-output", "--max-items", limit)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Equal(t, map[string]string{"0": "", "1": "file_first\n", "-1": "file_first\nfile_second\n"}[limit], got.stdout)
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

// This fixture checks the existing public workflow, not live indexing acceptance.
// Callers explicitly inspect status before searching; the CLI does not poll here.
func TestMainVectorStoreSearchAfterIndexingWorkflow(t *testing.T) {
	const content = "synthetic vector content\n"
	const attributes = `{"category":"guides","revision":7,"active":true}`
	file := filepath.Join(t.TempDir(), "vector fixture.txt")
	require.NoError(t, os.WriteFile(file, []byte(content), 0600))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stage := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-should-retry", "false")
		wantMethod, wantPath := http.MethodPost, ""
		response := ""
		switch stage {
		case 1:
			wantPath = "/files"
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			assert.Equal(t, "assistants", r.FormValue("purpose"))
			upload, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer upload.Close()
			data, err := io.ReadAll(upload)
			assert.NoError(t, err)
			assert.Equal(t, content, string(data))
			assert.Equal(t, filepath.Base(file), header.Filename)
			response = `{"id":"file_synthetic","object":"file","filename":"vector fixture.txt","purpose":"assistants","status":"uploaded"}`
		case 2:
			wantPath = "/vector_stores"
			response = `{"id":"vs_synthetic","object":"vector_store","name":"Synthetic guides","status":"completed"}`
		case 3:
			wantPath = "/vector_stores/vs_synthetic/files"
			data, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"file_id":"file_synthetic","attributes":`+attributes+`}`, string(data))
			response = `{"id":"file_synthetic","object":"vector_store.file","vector_store_id":"vs_synthetic","status":"in_progress","attributes":` + attributes + `}`
		case 4, 5:
			wantMethod, wantPath = http.MethodGet, "/vector_stores/vs_synthetic/files/file_synthetic"
			status := "in_progress"
			if stage == 5 {
				status = "completed"
			}
			response = `{"id":"file_synthetic","object":"vector_store.file","vector_store_id":"vs_synthetic","status":"` + status + `","attributes":` + attributes + `}`
		case 6:
			wantPath = "/vector_stores/vs_synthetic/search"
			data, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"query":"synthetic vector content","filters":{"type":"eq","key":"category","value":"guides"}}`, string(data))
			response = `{"object":"vector_store.search_results.page","data":[{"file_id":"file_synthetic","filename":"vector fixture.txt","score":0.95,"attributes":` + attributes + `,"content":[{"type":"text","text":"synthetic vector content\n"}]}]}`
		default:
			t.Errorf("unexpected workflow stage %d", stage)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, wantMethod, r.Method)
		assert.Equal(t, wantPath, r.URL.Path)
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	command := func(args ...string) map[string]json.RawMessage {
		t.Helper()
		got := runVectorStoreCommand(t, server, nil, append([]string{"--format", "json"}, args...)...)
		require.Zero(t, got.code, "%+v", got)
		require.Empty(t, got.stderr)
		var result map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(got.stdout), &result))
		return result
	}
	stringField := func(result map[string]json.RawMessage, name string) string {
		t.Helper()
		var value string
		require.NoError(t, json.Unmarshal(result[name], &value))
		return value
	}
	fileID := stringField(command("files", "upload", file, "--purpose", "assistants"), "id")
	storeID := stringField(command("vector-stores", "create", "--name", "Synthetic guides"), "id")
	attached := command("vector-stores:files", "create", "--vector-store-id", storeID, "--file-id", fileID, "--attributes", attributes)
	require.Equal(t, "in_progress", stringField(attached, "status"))
	for _, status := range []string{"in_progress", "completed"} {
		result := command("vector-stores", "files", "retrieve", "--vector-store-id", storeID, "--file-id", fileID)
		require.Equal(t, status, stringField(result, "status"))
		require.JSONEq(t, attributes, string(result["attributes"]))
	}
	result := command("vector-stores", "search", "--vector-store-id", storeID, "--query", "synthetic vector content", "--filters", `{"type":"eq","key":"category","value":"guides"}`)
	require.Equal(t, fileID, stringField(result, "file_id"))
	require.Equal(t, "vector fixture.txt", stringField(result, "filename"))
	require.JSONEq(t, attributes, string(result["attributes"]))
	require.JSONEq(t, `[{"type":"text","text":"synthetic vector content\n"}]`, string(result["content"]))
	require.EqualValues(t, 6, requests.Load())
}

func TestMainVectorStoreSearchPreservesRequestConfiguration(t *testing.T) {
	queryFile := filepath.Join(t.TempDir(), "query with spaces.txt")
	require.NoError(t, os.WriteFile(queryFile, []byte("file query\n"), 0600))
	for _, tc := range []struct {
		name, stdin, want string
		flags             []string
	}{
		{"string", "", `{"query":"synthetic query"}`, []string{"--query", "synthetic query"}},
		{"array", "", `{"query":["first query","second query"]}`, []string{"--query", `["first query","second query"]`}},
		{"JSON body", `{"query":["body query"],"filters":{"type":"eq","key":"category","value":"guides"},"future_input":null}`, `{"query":["body query"],"filters":{"type":"eq","key":"category","value":"guides"},"future_input":null}`, nil},
		{"YAML body", "query: body query\nrewrite_query: false\n", `{"query":"body query","rewrite_query":false}`, nil},
		{"file query", "", `{"query":"file query\n"}`, []string{"--query", "@" + queryFile}},
		{"overrides", `{"query":"body query","rewrite_query":true,"max_num_results":10}`, `{"query":"flag query","rewrite_query":false,"max_num_results":2,"filters":{"type":"eq","key":"active","value":true},"ranking_options":{"ranker":"none","score_threshold":0}}`, []string{"--query", "flag query", "--rewrite-query=false", "--max-num-results", "2", "--filters", `{"type":"eq","key":"active","value":true}`, "--ranking-options.ranker", "none", "--ranking-options.score-threshold", "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/custom/v1/vector_stores/vs_synthetic/search" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				for name, want := range map[string]string{"Authorization": "Bearer sk-fake-explicit-vector-key", "OpenAI-Organization": "org_synthetic", "OpenAI-Project": "proj_synthetic", "X-Synthetic": "preserved", "Content-Type": "application/json"} {
					if got := r.Header.Get(name); got != want {
						t.Errorf("request header %s was not preserved", name)
					}
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				} else {
					assert.JSONEq(t, tc.want, string(body))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, vectorStoreEmptySearch)
			}))
			defer server.Close()
			args := []string{"--base-url", server.URL + "/custom/v1", "--api-key", "sk-fake-explicit-vector-key", "--organization", "org_synthetic", "--project", "proj_synthetic", "--header", "X-Synthetic: preserved", "vector-stores", "search", "--vector-store-id", "vs_synthetic"}
			var input *os.File
			if tc.stdin != "" {
				input = shellFileInput(t, []byte(tc.stdin))
			}
			got := runVectorStoreCommand(t, server, input, append(args, tc.flags...)...)
			require.Equal(t, mainDispatchResult{0, vectorStoreNoMatches, ""}, got)
			require.EqualValues(t, 1, requests.Load())
		})
	}
}
