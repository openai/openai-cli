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

	"github.com/stretchr/testify/require"
)

const fineTuningEmptyPage = `{"object":"list","data":[],"has_more":false}`
const fineTuningJob = `{"id":"ftjob_example","object":"fine_tuning.job","status":"paused","model":"synthetic-model","error":null}`
const fineTuningEventPage = `{"object":"list","data":[{"id":"ftevent_example","object":"fine_tuning.job.event","created_at":17,"level":"info","message":"Synthetic training paused","type":"message"}],"has_more":false}`
const fineTuningCheckpointPage = `{"object":"list","data":[{"id":"ftckpt_example","object":"fine_tuning.job.checkpoint","created_at":17,"fine_tuning_job_id":"ftjob_example","fine_tuned_model_checkpoint":"ft:synthetic:checkpoint","step_number":1,"metrics":{"train_loss":0.5}}],"has_more":false}`
const fineTuningPermissionPage = `{"object":"list","data":[{"id":"perm_example","object":"checkpoint.permission","created_at":17,"project_id":"proj_shared"}],"has_more":false}`
const fineTuningEmptyMessage = "No fine-tuning jobs returned.\nTraining eligibility was not checked.\n"

func TestMainFineTuningEmptyJobsModes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/fine_tuning/jobs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fineTuningEmptyPage)
	}))
	defer server.Close()
	for _, route := range [][]string{{"fine-tuning", "jobs", "list"}, {"fine-tuning:jobs", "list"}} {
		for _, tc := range []struct {
			name, want string
			flags      []string
		}{
			{"default", fineTuningEmptyMessage, nil},
			{"auto", fineTuningEmptyMessage, []string{"--format", "auto"}},
			{"text", fineTuningEmptyMessage, []string{"--format", "TeXt"}},
			{"quiet", fineTuningEmptyMessage, []string{"--quiet"}},
			{"JSON", "", []string{"--format", "json"}},
			{"JSONL", "", []string{"--format", "jsonl"}},
			{"YAML", "", []string{"--format", "yaml"}},
			{"raw envelope", fineTuningEmptyPage + "\n", []string{"--format", "RaW"}},
			{"extraction", "", []string{"--transform", "id"}},
			{"text extraction", "No results.\n", []string{"--format", "text", "--transform", "id"}},
			{"raw output", "", []string{"--raw-output"}},
			{"text raw output", "No results.\n", []string{"--format", "text", "--raw-output"}},
			{"zero limit", "", []string{"--max-items", "0"}},
			{"unlimited", fineTuningEmptyMessage, []string{"--max-items", "-1"}},
		} {
			t.Run(strings.Join(route, "/")+"/"+tc.name, func(t *testing.T) {
				before := requests.Load()
				got := runReadableCommand(t, server, append(append([]string{}, route...), tc.flags...)...)
				require.Equal(t, mainDispatchResult{stdout: tc.want}, got)
				require.Equal(t, before+1, requests.Load())
			})
		}
	}
}

func TestMainFineTuningNonemptyJobsPreserveData(t *testing.T) {
	const page = `{"object":"list","data":[` + fineTuningJob + `],"has_more":false}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, page)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, want string
		flags      []string
	}{
		{"readable", "", nil},
		{"JSON", fineTuningJob, []string{"--format", "json"}},
		{"JSONL", fineTuningJob, []string{"--format", "jsonl"}},
		{"raw envelope", page, []string{"--format", "raw"}},
		{"extraction", `"ftjob_example"`, []string{"--transform", "id"}},
		{"raw extraction", "ftjob_example", []string{"--transform", "id", "--raw-output"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runReadableCommand(t, server, append([]string{"fine-tuning", "jobs", "list"}, tc.flags...)...)
			require.Zero(t, got.code, "%+v", got)
			require.NotContains(t, got.stdout+got.stderr, "No fine-tuning jobs")
			if tc.name == "readable" {
				require.Contains(t, got.stdout, "ID: ftjob_example\n")
				require.Contains(t, got.stdout, "Status: paused\n")
			} else {
				require.Empty(t, got.stderr)
				if json.Valid([]byte(tc.want)) {
					require.JSONEq(t, tc.want, got.stdout)
				} else {
					require.Equal(t, tc.want+"\n", got.stdout)
				}
			}
		})
	}
}

func TestMainFineTuningFailuresStayFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"authentication", `{"error":{"message":"Synthetic access failure","type":"invalid_request_error","code":"invalid_api_key"}}`, http.StatusUnauthorized},
		{"permission", `{"error":{"message":"Synthetic access failure","type":"invalid_request_error","code":"permission_denied"}}`, http.StatusForbidden},
		{"empty error", `{}`, http.StatusForbidden},
		{"malformed error", `{"error":`, http.StatusForbidden},
		{"malformed success", `{"data":`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			for _, flags := range [][]string{nil, {"--format-error", "json"}, {"--max-items", "0"}} {
				got := runReadableCommand(t, server, append([]string{"fine-tuning", "jobs", "list"}, flags...)...)
				require.Equal(t, 1, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				require.NotEmpty(t, got.stderr)
				require.NotContains(t, got.stderr, "Training eligibility")
				if len(flags) > 0 && flags[0] == "--format-error" {
					require.True(t, json.Valid([]byte(got.stderr)), "stderr=%q", got.stderr)
				}
			}
		})
	}
}

func TestMainFineTuningPaginationPreservesLateFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			_, _ = io.WriteString(w, `{"object":"list","data":[`+fineTuningJob+`],"has_more":true,"last_id":"ftjob_example"}`)
			return
		}
		if r.URL.Query().Get("after") != "ftjob_example" {
			t.Errorf("unexpected cursor: %q", r.URL.Query().Get("after"))
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"Synthetic second-page failure","code":"permission_denied"}}`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		limit string
		calls int32
		code  int
	}{{"0", 1, 0}, {"1", 1, 0}, {"-1", 2, 1}} {
		t.Run(tc.limit, func(t *testing.T) {
			requests.Store(0)
			got := runReadableCommand(t, server, "fine-tuning:jobs", "list", "--max-items", tc.limit)
			require.Equal(t, tc.code, got.code, "%+v", got)
			require.Equal(t, tc.calls, requests.Load())
			require.NotContains(t, got.stdout+got.stderr, "Training eligibility")
			if tc.limit == "0" {
				require.Empty(t, got.stdout)
			} else {
				require.Contains(t, got.stdout, "ID: ftjob_example\n")
			}
			if tc.code != 0 {
				require.Contains(t, got.stderr, "HTTP 403")
			}
		})
	}
}

func TestMainFineTuningMonitoringAndSharingRequests(t *testing.T) {
	for _, tc := range []struct {
		group, method, verb, path, response string
		args                                []string
		admin                               bool
	}{
		{"jobs", "list", "GET", "/fine_tuning/jobs", fineTuningEmptyPage, []string{"--after", "ftjob_cursor", "--limit", "2", "--metadata", `{"source":"synthetic"}`}, false},
		{"jobs", "retrieve", "GET", "/fine_tuning/jobs/ftjob_example", fineTuningJob, []string{"--fine-tuning-job-id", "ftjob_example"}, false},
		{"jobs", "list-events", "GET", "/fine_tuning/jobs/ftjob_example/events", fineTuningEventPage, []string{"--fine-tuning-job-id", "ftjob_example", "--after", "ftevent_example", "--limit", "2"}, false},
		{"jobs", "pause", "POST", "/fine_tuning/jobs/ftjob_example/pause", fineTuningJob, []string{"--fine-tuning-job-id", "ftjob_example"}, false},
		{"jobs", "resume", "POST", "/fine_tuning/jobs/ftjob_example/resume", fineTuningJob, []string{"--fine-tuning-job-id", "ftjob_example"}, false},
		{"jobs:checkpoints", "list", "GET", "/fine_tuning/jobs/ftjob_example/checkpoints", fineTuningCheckpointPage, []string{"--fine-tuning-job-id", "ftjob_example"}, false},
		{"checkpoints:permissions", "list", "GET", "/fine_tuning/checkpoints/ft:synthetic:checkpoint/permissions", fineTuningPermissionPage, []string{"--fine-tuned-model-checkpoint", "ft:synthetic:checkpoint", "--project-id", "proj_shared", "--order", "ascending"}, true},
		{"checkpoints:permissions", "create", "POST", "/fine_tuning/checkpoints/ft:synthetic:checkpoint/permissions", fineTuningPermissionPage, []string{"--fine-tuned-model-checkpoint", "ft:synthetic:checkpoint", "--project-id", "proj_shared", "--project-id", "proj_second"}, true},
		{"checkpoints:permissions", "delete", "DELETE", "/fine_tuning/checkpoints/ft:synthetic:checkpoint/permissions/perm_example", `{"id":"perm_example","object":"checkpoint.permission","deleted":true}`, []string{"--fine-tuned-model-checkpoint", "ft:synthetic:checkpoint", "--permission-id", "perm_example"}, true},
	} {
		for _, colon := range []bool{false, true} {
			name := tc.group + "/" + tc.method
			if colon {
				name += "/colon"
			}
			t.Run(name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != tc.verb || r.URL.Path != tc.path {
						t.Errorf("request = %s %s; want %s %s", r.Method, r.URL.Path, tc.verb, tc.path)
					}
					key := "synthetic-api-explicit"
					if tc.admin {
						key = "synthetic-admin-explicit"
					}
					for header, want := range map[string]string{"Authorization": "Bearer " + key, "OpenAI-Organization": "org_explicit", "OpenAI-Project": "proj_header", "X-Synthetic-Custom": "explicit", "X-Synthetic-Only": "environment"} {
						if r.Header.Get(header) != want {
							t.Errorf("request did not preserve %s override", header)
						}
					}
					if tc.method == "list-events" && (r.URL.Query().Get("after") != "ftevent_example" || r.URL.Query().Get("limit") != "2") {
						t.Error("event pagination options changed")
					}
					if tc.group == "jobs" && tc.method == "list" && (r.URL.Query().Get("after") != "ftjob_cursor" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("metadata[source]") != "synthetic") {
						t.Error("job list filters changed")
					}
					if tc.admin && tc.method == "list" && (r.URL.Query().Get("project_id") != "proj_shared" || r.URL.Query().Get("order") != "ascending") {
						t.Error("sharing permission filters changed")
					}
					if tc.admin && tc.method == "create" {
						var body struct {
							ProjectIDs []string `json:"project_ids"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.Join(body.ProjectIDs, ",") != "proj_shared,proj_second" {
							t.Errorf("sharing body changed: %+v, error=%v", body, err)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, tc.response)
				}))
				defer server.Close()
				args := []string{"openai", "--base-url", server.URL, "--api-key", "synthetic-api-explicit", "--admin-api-key", "synthetic-admin-explicit", "--organization", "org_explicit", "--project", "proj_explicit", "--header", "OpenAI-Project: proj_header", "--header", "X-Synthetic-Custom: explicit", "--format", "raw"}
				if colon {
					args = append(args, "fine-tuning:"+tc.group)
				} else {
					args = append(args, "fine-tuning")
					args = append(args, strings.Split(tc.group, ":")...)
				}
				args = append(args, tc.method)
				args = append(args, tc.args...)
				got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=invalid", "OPENAI_API_KEY=synthetic-api-env", "OPENAI_ADMIN_KEY=synthetic-admin-env", "OPENAI_ORG_ID=org_env", "OPENAI_PROJECT_ID=proj_env", "OPENAI_CUSTOM_HEADERS=X-Synthetic-Custom: environment\nX-Synthetic-Only: environment", "FORCE_COLOR=0"}, args...)
				require.Equal(t, mainDispatchResult{stdout: tc.response + "\n"}, got)
				require.EqualValues(t, 1, requests.Load())
			})
		}
	}
}

func TestMainFineTuningJobBodyInputs(t *testing.T) {
	trainingPath := filepath.Join(t.TempDir(), "training id.txt")
	require.NoError(t, os.WriteFile(trainingPath, []byte("file_training"), 0o600))
	for _, tc := range []struct {
		name, input string
		flags       []string
	}{
		{"flags", "", []string{"--model", "synthetic-model", "--training-file", "file_training", "--metadata", `{"source":"synthetic"}`}},
		{"file", "", []string{"--model", "synthetic-model", "--training-file", "@" + trainingPath, "--metadata", `{"source":"synthetic"}`}},
		{"JSON override", `{"model":"overridden-model","training_file":"file_training","metadata":{"source":"synthetic"},"seed":null}`, []string{"--model", "synthetic-model"}},
		{"YAML", "model: synthetic-model\ntraining_file: file_training\nmetadata:\n  source: synthetic\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/fine_tuning/jobs" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != "synthetic-model" || body["training_file"] != "file_training" {
					t.Errorf("job inputs changed: %+v", body)
				}
				metadata, ok := body["metadata"].(map[string]any)
				if !ok || metadata["source"] != "synthetic" {
					t.Errorf("metadata changed: %+v", body["metadata"])
				}
				if tc.name == "JSON override" {
					if seed, exists := body["seed"]; !exists || seed != nil {
						t.Error("explicit null seed changed")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, fineTuningJob)
			}))
			defer server.Close()
			var input *os.File
			if tc.input != "" {
				path := filepath.Join(t.TempDir(), "input.json")
				require.NoError(t, os.WriteFile(path, []byte(tc.input), 0o600))
				var err error
				input, err = os.Open(path)
				require.NoError(t, err)
				defer input.Close()
			}
			args := append([]string{"openai", "fine-tuning", "jobs", "create", "--format", "json"}, tc.flags...)
			got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-api-key", "FORCE_COLOR=0"}, input, args...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.JSONEq(t, fineTuningJob, got.stdout)
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestMainFineTuningHelpPreservesScope(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"fine-tuning", []string{"Training access is restricted.", "https://developers.openai.com/api/docs/deprecations#update-to-openais-self-serve-fine-tuning"}},
		{"fine-tuning jobs", []string{"list-events", "training eligibility"}},
		{"fine-tuning jobs list", []string{"metadata filters", "--after", "--max-items"}},
		{"fine-tuning jobs retrieve", []string{"list-events", "--format json"}},
		{"fine-tuning jobs list-events", []string{"does not wait or follow future events", "--after"}},
		{"fine-tuning jobs pause", []string{"API decides", "returned status"}},
		{"fine-tuning jobs resume", []string{"can incur charges", "returned status"}},
		{"fine-tuning jobs checkpoints", []string{"does not download model weights"}},
		{"fine-tuning checkpoints permissions", []string{"same organization", "admin API key", "no checkpoint export command"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			path := strings.Fields(tc.path)
			canonical := []string{strings.Join(path, ":")}
			switch path[len(path)-1] {
			case "list", "retrieve", "list-events", "pause", "resume":
				canonical = []string{strings.Join(path[:len(path)-1], ":"), path[len(path)-1]}
			}
			for _, route := range [][]string{path, canonical} {
				got := runMainDispatch(t, "bash", append(append([]string{"openai"}, route...), "--help")...)
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
				plain := strings.Join(strings.Fields(got.stdout), " ")
				for _, want := range tc.want {
					require.Contains(t, plain, want)
				}
			}
		})
	}
	got := runMainDispatch(t, "bash", "openai", "fine-tuning", "checkpoints", "export", "--help")
	require.Equal(t, 3, got.code)
	require.Empty(t, got.stdout)
	require.Contains(t, got.stderr, "Unknown help topic")
}

func TestMainFineTuningHelpExamplesExecute(t *testing.T) {
	for _, tc := range []struct{ topic, path, response string }{
		{"fine-tuning jobs list", "/fine_tuning/jobs", fineTuningEmptyPage},
		{"fine-tuning jobs retrieve", "/fine_tuning/jobs/ftjob_example", fineTuningJob},
		{"fine-tuning jobs list-events", "/fine_tuning/jobs/ftjob_example/events", fineTuningEventPage},
		{"fine-tuning jobs checkpoints", "/fine_tuning/jobs/ftjob_example/checkpoints", fineTuningCheckpointPage},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != tc.path {
					t.Errorf("printed example reached %s %s; want GET %s", r.Method, r.URL.Path, tc.path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			help := runMainDispatch(t, "bash", append(append([]string{"openai"}, strings.Fields(tc.topic)...), "--help")...)
			require.Zero(t, help.code, "%+v", help)
			_, exampleSection, found := strings.Cut(help.stdout, "\nEXAMPLES:\n")
			require.True(t, found, "help=%q", help.stdout)
			var examples []string
			for _, line := range strings.Split(exampleSection, "\n") {
				if line = strings.TrimSpace(line); strings.HasPrefix(line, "openai fine-tuning ") {
					examples = append(examples, line)
				}
			}
			require.Len(t, examples, 1, "help=%q", help.stdout)
			// These authored examples contain only unquoted command and ID tokens.
			got := runReadableCommand(t, server, strings.Fields(examples[0])[1:]...)
			require.Zero(t, got.code, "%+v", got)
			require.NotEmpty(t, got.stdout)
			require.EqualValues(t, 1, requests.Load())
		})
	}
}
