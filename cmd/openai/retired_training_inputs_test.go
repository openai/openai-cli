package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainTrainingInputsAreUnavailable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	file := filepath.Join(dir, "synthetic-private-upload.txt")
	purpose := filepath.Join(dir, "purpose.txt")
	event := filepath.Join(dir, "event.txt")
	for path, data := range map[string]string{file: "synthetic-private-content", purpose: "fine-tune", event: "fine_tuning.job.succeeded"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const fileError = "This file purpose is unavailable in the CLI. Use the Platform API directly."
	const eventError = "This webhook request contains events unavailable in the CLI. Use the Platform API directly."
	uploadArgs := []string{"uploads", "create", "--filename", "synthetic-private-upload.txt", "--bytes", "25", "--mime-type", "text/plain"}
	for _, test := range []struct {
		name, input, message string
		args                 []string
	}{
		{"upload purpose", "", fileError, []string{"files", "upload", file, "--purpose", "fine-tune"}},
		{"scalar purpose file", "", fileError, []string{"files", "upload", file, "--purpose", "@" + purpose}},
		{"file JSON", `{"purpose":"fine-tune"}`, fileError, []string{"files", "create", "--file", file}},
		{"file YAML", "purpose: fine-tune\n", fileError, []string{"files", "upload", file}},
		{"trusted piped scalar", retirementPurposeFileInput(t, purpose), fileError, []string{"files", "create", "--file", file}},
		{"upload JSON", `{"purpose":"fine-tune"}`, fileError, uploadArgs},
		{"upload YAML", "purpose: fine-tune\n", fileError, uploadArgs},
		{"upload scalar", "", fileError, append(append([]string{}, uploadArgs...), "--purpose", "@"+purpose)},
		{"file query", "", fileError, []string{"files", "list", "--purpose", "fine-tune"}},
		{"result query", "", fileError, []string{"files", "list", "--purpose", "fine-tune-results"}},
		{"query scalar file", "", fileError, []string{"files", "list", "--purpose", "@" + purpose}},
		{"webhook mixed create", "", eventError, []string{"webhooks", "create", "--name", "synthetic-private-endpoint", "--url", "https://synthetic.invalid/", "--event-type", "response.completed", "--event-type", "fine_tuning.job.succeeded"}},
		{"webhook mixed update", `{"event_types":["response.completed","fine_tuning.job.failed"]}`, eventError, []string{"webhooks", "update", "synthetic-private-endpoint"}},
		{"webhook update YAML", "event_types:\n  - fine_tuning.job.cancelled\n", eventError, []string{"webhooks", "update", "synthetic-private-endpoint"}},
		{"webhook test scalar", "", eventError, []string{"webhooks", "test", "synthetic-private-endpoint", "--event-type", "@" + event}},
		{"webhook test JSON", `{"event_type":"fine_tuning.job.succeeded"}`, eventError, []string{"webhooks", "test", "synthetic-private-endpoint"}},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				stdin := retirementStdin(t, test.input)
				env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-private-key", "OPENAI_UNTRUSTED_STDIN=false"}
				args := append([]string{"openai", "--format-error", format}, test.args...)
				got := runMainDispatchWithStdin(t, "bash", env, stdin, args...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("retired input = %+v; want exit 1 and no stdout", got)
				}
				message := strings.TrimSpace(got.stderr)
				if format == "json" {
					payload := decodeMainStructuredError(t, format, got.stderr)
					message, _ = payload["message"].(string)
					if _, exists := payload["status_code"]; exists {
						t.Error("local rejection invented an HTTP status")
					}
				}
				if message != test.message || strings.Contains(got.stderr, "synthetic-private") {
					t.Fatalf("retired input diagnostic = %q; want %q", message, test.message)
				}
				if count := requests.Load(); count != 0 {
					t.Fatalf("retired inputs sent %d requests", count)
				}
			})
		}
	}
}

func TestMainModelsDeleteIsUnavailable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-private-key"}
	for _, args := range [][]string{
		{"models", "delete", "synthetic-private-model"},
		{"models", "delete", "--model", "synthetic-private-model"},
		{"models", "delete", "--help"},
		{"help", "--all", "models", "delete"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
		unknown := strings.HasPrefix(got.stderr, "Unknown command.") || strings.HasPrefix(got.stderr, "Unknown help topic.") ||
			strings.HasPrefix(got.stderr, "The --model option is not available for this command.")
		if (got.code != 1 && got.code != 3) || !unknown || got.stdout != "" || strings.Contains(got.stderr, "synthetic-private") {
			t.Errorf("models delete remains available: %+v", got)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("models delete sent a request")
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		got := runMainDispatch(t, style, mainCompletionArgs(style, "models", "")...)
		if got.code != 0 || strings.Contains(got.stdout, "delete") || !strings.Contains(got.stdout, "list") {
			t.Errorf("models completion = %+v", got)
		}
	}
}

func TestMainTrainingReferencesAreAbsentFromHelp(t *testing.T) {
	for _, route := range [][]string{
		{"files", "upload"}, {"files", "create"}, {"uploads", "create"},
		{"chat", "completions", "create"}, {"chat:completions", "create"}, {"models"},
	} {
		got := runMainDispatch(t, "bash", append([]string{"openai", "help", "--all"}, route...)...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("full help %v = %+v", route, got)
		}
		// Startup can retain the test executable in a custom usage synopsis.
		// Its temporary directory may contain the retired feature's name.
		help := strings.ReplaceAll(got.stdout, os.Args[0], "openai")
		for _, forbidden := range []string{"fine-tun", "fine_tun", "distillation", "vision fine"} {
			if strings.Contains(strings.ToLower(help), forbidden) {
				t.Errorf("help %v retains %q:\n%s", route, forbidden, help)
			}
		}
		for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
			args := append(append([]string{}, route...), "--")
			completion := runMainDispatch(t, style, mainCompletionArgs(style, args...)...)
			if completion.code != 0 || completion.stderr != "" || strings.Contains(completion.stdout, "fine-tun") || strings.Contains(completion.stdout, "distillation") {
				t.Errorf("%s flag completion %v = %+v", style, route, completion)
			}
		}
	}
}

func TestMainRetirementPreservesSharedAPIData(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("purpose") != "batch" {
				t.Errorf("query changed: %s", r.URL.RawQuery)
			}
			io.WriteString(w, `{"object":"list","data":[{"id":"file_synthetic","object":"file","purpose":"fine-tune"}],"has_more":false}`)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		purpose := r.FormValue("purpose")
		if purpose == "" {
			part, _, err := r.FormFile("purpose")
			if err != nil {
				t.Error(err)
				return
			}
			data, err := io.ReadAll(part)
			part.Close()
			if err != nil {
				t.Error(err)
			}
			purpose = string(data)
		}
		if purpose != "batch" {
			t.Errorf("purpose changed: %q", purpose)
		}
		io.WriteString(w, `{"id":"file_synthetic","object":"file","purpose":"batch"}`)
	}))
	t.Cleanup(server.Close)
	file, purpose := filepath.Join(t.TempDir(), "sample.txt"), filepath.Join(t.TempDir(), "purpose.txt")
	for path, value := range map[string]string{file: "fine-tune is arbitrary user data here", purpose: "batch"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-key"}
	for _, args := range [][]string{
		{"files", "upload", file, "--purpose", "batch"},
		{"files", "create", "--file", file, "--purpose", "@" + purpose},
		{"files", "list", "--purpose", "batch"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--format", "json"}, args...)...)
		if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) {
			t.Fatalf("shared route = %+v", got)
		}
		if args[1] == "list" && !strings.Contains(got.stdout, "fine-tune") {
			t.Fatal("shared API response lost historical data")
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("shared requests = %d; want 3", requests.Load())
	}
}

func TestMainRetirementPreservesOpaqueModelIDs(t *testing.T) {
	const model = "ft:synthetic-model:example:custom:abc"
	const metadata = `{"purpose":"fine-tune","filename":"fine_tuning.jsonl"}`
	const object = `{"id":"` + model + `","object":"model","created":1,"owned_by":"synthetic","metadata":` + metadata + `}`
	const response = `{"id":"resp_synthetic","object":"response","model":"` + model + `","output":[],"metadata":` + metadata + `}`
	const completion = `{"id":"chatcmpl_synthetic","object":"chat.completion","model":"` + model + `","choices":[],"metadata":` + metadata + `}`
	const embedding = `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}],"model":"` + model + `","usage":{"prompt_tokens":1,"total_tokens":1}}`
	for _, test := range []struct {
		name, method, path, input, response, output string
		args                                        []string
	}{
		{"responses", "POST", "/responses", `{"model":"` + model + `","input":"fine-tuning is arbitrary text","metadata":` + metadata + `}`, response, response, []string{"responses", "create"}},
		{"chat completions", "POST", "/chat/completions", `{"model":"` + model + `","messages":[{"role":"user","content":"synthetic input"}],"metadata":` + metadata + `}`, completion, completion, []string{"chat", "completions", "create"}},
		{"embeddings", "POST", "/embeddings", `{"model":"` + model + `","input":"synthetic input"}`, embedding, embedding, []string{"embeddings", "create"}},
		{"model flag", "GET", "/models/" + model, "", object, object, []string{"models", "retrieve", "--model", model}},
		{"model positional", "GET", "/models/" + model, "", object, object, []string{"models", "retrieve", model}},
		{"model listing", "GET", "/models", "", `{"object":"list","data":[` + object + `]}`, `[` + object + `]`, []string{"models", "list"}},
		{"model extraction", "GET", "/models/" + model, "", object, model, []string{"models", "retrieve", model, "--transform", "id", "--raw-output"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s; want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if test.input != "" {
					var got, want any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if err := json.Unmarshal([]byte(test.input), &want); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("request body = %#v; want %#v", got, want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, test.response)
			}))
			t.Cleanup(server.Close)
			env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-key"}
			args := append([]string{"openai", "--format", "json"}, test.args...)
			got := runMainDispatchWithStdin(t, "bash", env, retirementStdin(t, test.input), args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("opaque model route = %+v; requests = %d", got, requests.Load())
			}
			if test.output == model {
				if strings.TrimSpace(got.stdout) != model {
					t.Fatalf("model extraction = %q; want %q", got.stdout, model)
				}
				return
			}
			var actual, expected any
			if err := json.Unmarshal([]byte(got.stdout), &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.output), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("model response = %#v; want %#v", actual, expected)
			}
		})
	}
}

func TestMainRetirementPreservesVisionInputs(t *testing.T) {
	const content = "synthetic image bytes"
	const fileObject = `{"id":"file_vision_synthetic","object":"file","bytes":21,"created_at":1,"filename":"image.png","purpose":"vision"}`
	file, purpose := filepath.Join(t.TempDir(), "image.png"), filepath.Join(t.TempDir(), "purpose.txt")
	for path, data := range map[string]string{file: content, purpose: "vision"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	uploadArgs := []string{"uploads", "create", "--filename", "image.png", "--bytes", "21", "--mime-type", "image/png"}
	for _, test := range []struct {
		name, input, method, path string
		args                      []string
	}{
		{"upload flag", "", "POST", "/files", []string{"files", "upload", file, "--purpose", "vision"}},
		{"create flag", "", "POST", "/files", []string{"files", "create", "--file", file, "--purpose", "vision"}},
		{"create JSON", `{"purpose":"vision"}`, "POST", "/files", []string{"files", "create", "--file", file}},
		{"upload YAML", "purpose: vision\n", "POST", "/files", []string{"files", "upload", file}},
		{"scalar file", "", "POST", "/files", []string{"files", "create", "--file", file, "--purpose", "@" + purpose}},
		{"piped scalar file", retirementPurposeFileInput(t, purpose), "POST", "/files", []string{"files", "create", "--file", file}},
		{"flag overrides input", `{"purpose":"fine-tune"}`, "POST", "/files", []string{"files", "create", "--file", file, "--purpose", "vision"}},
		{"list flag", "", "GET", "/files", []string{"files", "list", "--purpose", "vision"}},
		{"list scalar file", "", "GET", "/files", []string{"files", "list", "--purpose", "@" + purpose}},
		{"upload session flag", "", "POST", "/uploads", append(append([]string{}, uploadArgs...), "--purpose", "vision")},
		{"upload session JSON", `{"purpose":"vision"}`, "POST", "/uploads", uploadArgs},
		{"upload session YAML", "purpose: vision\n", "POST", "/uploads", uploadArgs},
		{"upload session scalar file", "", "POST", "/uploads", append(append([]string{}, uploadArgs...), "--purpose", "@"+purpose)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/responses" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					want := map[string]any{"model": "synthetic-vision-model", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "file_id": "file_vision_synthetic"}}}}}
					if r.Method != "POST" || !reflect.DeepEqual(body, want) {
						t.Errorf("image inference = %s %#v; want POST %#v", r.Method, body, want)
					}
					io.WriteString(w, `{"id":"resp_vision_synthetic","object":"response","output":[]}`)
					return
				}
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s; want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if r.Method == "GET" {
					if r.URL.Query().Get("purpose") != "vision" {
						t.Errorf("purpose query = %q", r.URL.RawQuery)
					}
					io.WriteString(w, `{"object":"list","data":[`+fileObject+`],"has_more":false}`)
					return
				}
				if r.URL.Path == "/uploads" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					want := map[string]any{"purpose": "vision", "filename": "image.png", "bytes": float64(len(content)), "mime_type": "image/png"}
					if !reflect.DeepEqual(body, want) {
						t.Errorf("upload body = %#v; want %#v", body, want)
					}
					io.WriteString(w, `{"id":"upload_vision_synthetic","object":"upload","status":"pending","bytes":21,"created_at":1,"expires_at":3601,"filename":"image.png","purpose":"vision"}`)
					return
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				for field, want := range map[string]string{"purpose": "vision", "file": content} {
					value := r.FormValue(field)
					if value == "" {
						part, _, err := r.FormFile(field)
						if err != nil {
							t.Error(err)
							return
						}
						data, err := io.ReadAll(part)
						part.Close()
						if err != nil {
							t.Error(err)
						}
						value = string(data)
					}
					if value != want {
						t.Errorf("%s = %q; want %q", field, value, want)
					}
				}
				io.WriteString(w, fileObject)
			}))
			t.Cleanup(server.Close)
			env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-key", "OPENAI_UNTRUSTED_STDIN=false"}
			args := append([]string{"openai", "--format", "json"}, test.args...)
			got := runMainDispatchWithStdin(t, "bash", env, retirementStdin(t, test.input), args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 || !json.Valid([]byte(got.stdout)) {
				t.Fatalf("vision route = %+v; requests = %d", got, requests.Load())
			}
			if test.method != "POST" || test.path != "/files" {
				return
			}
			var uploaded struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(got.stdout), &uploaded); err != nil || uploaded.ID != "file_vision_synthetic" {
				t.Fatalf("uploaded file = %q; decode error = %v", uploaded.ID, err)
			}
			input := `{"model":"synthetic-vision-model","input":[{"role":"user","content":[{"type":"input_image","file_id":"` + uploaded.ID + `"}]}]}`
			inference := runMainDispatchWithStdin(t, "bash", env, retirementStdin(t, input), "openai", "--format", "json", "responses", "create")
			if inference.code != 0 || inference.stderr != "" || requests.Load() != 2 || !strings.Contains(inference.stdout, "resp_vision_synthetic") {
				t.Fatalf("image inference = %+v; requests = %d", inference, requests.Load())
			}
		})
	}
}

func retirementPurposeFileInput(t *testing.T, path string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"purpose": "@" + path})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func retirementStdin(t *testing.T, contents string) *os.File {
	t.Helper()
	if contents == "" {
		return nil
	}
	file, err := os.CreateTemp(t.TempDir(), "stdin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	if _, err := file.WriteString(contents); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return file
}
