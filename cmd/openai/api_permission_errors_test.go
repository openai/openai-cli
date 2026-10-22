package main

import (
	"encoding/json"
	"fmt"
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

const mainFilesWritePermissionMessage = "You have insufficient permissions for this operation. Missing scopes: api.files.write. Check synthetic-private-guidance at https://untrusted.invalid/reset?token=synthetic-secret."

func mainPermissionErrorDetails(t *testing.T, message string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"message": message,
		"type":    "invalid_request_error",
		"code":    "insufficient_permissions",
		"param":   nil,
		"future_field": map[string]any{
			"sequence": json.Number("9007199254740993"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertMainPermissionGuidance(t *testing.T, output string, upload bool) {
	t.Helper()
	operation := "The API denied this request."
	if upload {
		operation = "File upload was denied."
	}
	for _, fragment := range []string{
		operation,
		"The API reports missing permission: api.files.write.",
		"https://platform.openai.com/api-keys",
		"Files Write", "Batch Write", "project owner",
		"Correct access before retrying the failed operation.",
		"API error details: --format-error json.",
	} {
		if !strings.Contains(output, fragment) {
			t.Errorf("permission recovery omitted %q: %q", fragment, output)
		}
	}
	for _, private := range []string{"synthetic-private-guidance", "untrusted.invalid", "synthetic-secret", "sk-fake-readable-test"} {
		if strings.Contains(output, private) {
			t.Errorf("human guidance exposed %q: %q", private, output)
		}
	}
}

func TestMainDispatchAPIPermissionUploadRecovery(t *testing.T) {
	const input = "{\"custom_id\":\"synthetic-request\"}\n"
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, operation := range []string{"upload", "create"} {
			t.Run(fmt.Sprintf("%d/%s", status, operation), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "input with spaces.jsonl")
				if err := os.WriteFile(path, []byte(input), 0600); err != nil {
					t.Fatal(err)
				}
				body := `{"error":` + mainPermissionErrorDetails(t, mainFilesWritePermissionMessage) + `}`
				var requests atomic.Int32
				var corrected atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/files" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.Error(w, "unexpected request", http.StatusBadRequest)
						return
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Errorf("upload is not multipart: %v", err)
						http.Error(w, "invalid multipart", http.StatusBadRequest)
						return
					}
					defer r.MultipartForm.RemoveAll()
					file, header, err := r.FormFile("file")
					if err != nil {
						t.Errorf("upload file is missing: %v", err)
						http.Error(w, "missing file", http.StatusBadRequest)
						return
					}
					defer file.Close()
					data, err := io.ReadAll(file)
					if err != nil || string(data) != input || header.Filename != filepath.Base(path) || r.FormValue("purpose") != "batch" {
						t.Errorf("upload changed: filename=%q purpose=%q bytes=%q error=%v", header.Filename, r.FormValue("purpose"), data, err)
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("x-should-retry", "false")
					w.Header().Set("x-request-id", "req_synthetic_permission")
					if !corrected.Load() {
						w.WriteHeader(status)
						fmt.Fprint(w, body)
						return
					}
					fmt.Fprint(w, `{"id":"file_permission","object":"file","bytes":34,"created_at":1700000000,"filename":"input with spaces.jsonl","purpose":"batch","status":"uploaded"}`)
				}))
				defer server.Close()
				args := []string{"files", operation, "--file", path, "--purpose", "batch"}
				denied := runReadableCommand(t, server, args...)
				if denied.code != 1 || denied.stdout != "" || requests.Load() != 1 {
					t.Fatalf("permission failure changed requests or exit: result=%+v requests=%d", denied, requests.Load())
				}
				assertMainPermissionGuidance(t, denied.stderr, true)
				for _, fragment := range []string{fmt.Sprintf("HTTP %d", status), "Request ID: req_synthetic_permission"} {
					if !strings.Contains(denied.stderr, fragment) {
						t.Errorf("permission recovery lost %q: %q", fragment, denied.stderr)
					}
				}
				// Follow the guidance using the identical command after the fixture
				// permits access. The error presenter must not retry on its own.
				corrected.Store(true)
				recovered := runReadableCommand(t, server, args...)
				if recovered.code != 0 || recovered.stderr != "" || !strings.Contains(recovered.stdout, "file_permission") || requests.Load() != 2 {
					t.Fatalf("corrected permission did not recover: result=%+v requests=%d", recovered, requests.Load())
				}
			})
		}
	}
}

func TestMainDispatchAPIPermissionOutputContracts(t *testing.T) {
	details := mainPermissionErrorDetails(t, mainFilesWritePermissionMessage)
	body := `{"error":` + details + `}`
	want := decodeMainErrorObject(t, "json", details)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, flag := range []string{"--format", "--format-error"} {
			for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
				t.Run(fmt.Sprintf("%d/%s/%s", status, flag, format), func(t *testing.T) {
					result := runMainAPIErrorResponse(t, status, "application/json", body, flag, format)
					if got := decodeMainErrorObject(t, format, result.stderr); !reflect.DeepEqual(got, want) {
						t.Errorf("permission error payload changed: got %#v; want %#v", got, want)
					}
					if strings.Contains(result.stderr, "https://platform.openai.com/api-keys") {
						t.Error("human guidance contaminated the structured error")
					}
				})
			}
		}
	}
	for _, flags := range [][]string{
		{"--transform-error", "future_field.sequence"},
		{"--format", "json", "--transform-error", "future_field.sequence"},
		{"--format-error", "text", "--transform-error", "future_field.sequence"},
	} {
		t.Run("extraction/"+strings.Join(flags, "/"), func(t *testing.T) {
			result := runMainAPIErrorResponse(t, http.StatusUnauthorized, "application/json", body, flags...)
			if strings.TrimSpace(result.stderr) != "9007199254740993" {
				t.Errorf("permission error extraction changed: %q", result.stderr)
			}
		})
	}
	for _, flags := range [][]string{
		nil,
		{"--quiet"},
		{"--format", "json", "--format-error", "text"},
		{"--format", "yaml", "--format-error", "auto"},
	} {
		t.Run("human/"+strings.Join(flags, "/"), func(t *testing.T) {
			result := runMainAPIErrorResponse(t, http.StatusForbidden, "application/json", body, flags...)
			assertMainPermissionGuidance(t, result.stderr, false)
		})
	}
	for _, flags := range [][]string{
		{"--quiet", "--format-error", "json"},
		{"--format", "text", "--format-error", "json"},
	} {
		t.Run("structured override/"+strings.Join(flags, "/"), func(t *testing.T) {
			result := runMainAPIErrorResponse(t, http.StatusForbidden, "application/json", body, flags...)
			if got := decodeMainErrorObject(t, "json", result.stderr); !reflect.DeepEqual(got, want) {
				t.Errorf("explicit structured error changed: got %#v; want %#v", got, want)
			}
		})
	}
}

func TestMainDispatchAPIPermissionUnknownDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, message, fallback string
		status                  int
	}{
		{"missing canonical prefix", "Missing scopes: api.files.write. synthetic-private-guidance", "Authentication failed for the custom API address.", http.StatusUnauthorized},
		{"unknown scope", "You have insufficient permissions for this operation. Missing scopes: api.models.read. synthetic-private-guidance", "Check your key's permissions.", http.StatusForbidden},
		{"malformed scope list", "You have insufficient permissions for this operation. Missing scopes: api.files.write, bad-scope. synthetic-private-guidance", "Check your key's permissions.", http.StatusForbidden},
		{"control character", mainFilesWritePermissionMessage + "\nforged guidance", "Authentication failed for the custom API address.", http.StatusUnauthorized},
		{"unrelated HTTP status", mainFilesWritePermissionMessage, "The API rejected the request.", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"error":` + mainPermissionErrorDetails(t, tc.message) + `}`
			result := runMainAPIErrorResponse(t, tc.status, "application/json", body)
			if !strings.Contains(result.stderr, tc.fallback) {
				t.Errorf("existing fallback changed: %q; want %q", result.stderr, tc.fallback)
			}
			for _, fragment := range []string{"api.files.write", "https://platform.openai.com/api-keys", "synthetic-private-guidance", "forged guidance", "untrusted.invalid"} {
				if strings.Contains(result.stderr, fragment) {
					t.Errorf("unsupported diagnostic produced %q: %q", fragment, result.stderr)
				}
			}
		})
	}
	t.Run("malformed JSON", func(t *testing.T) {
		result := runMainAPIErrorResponse(t, http.StatusUnauthorized, "application/json", `{"error":{"message":"You have insufficient permissions for this operation. Missing scopes: api.files.write.`)
		if strings.TrimSpace(result.stderr) == "" || strings.Contains(result.stderr, "api.files.write") || strings.Contains(result.stderr, "https://platform.openai.com/api-keys") {
			t.Errorf("malformed JSON changed generic error handling: %q", result.stderr)
		}
	})
}

func TestMainDispatchAPIPermissionMixedWebhookAndFiles(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, scope := range []string{"api.webhooks.read", "api.webhooks.write"} {
			t.Run(fmt.Sprintf("%d/%s", status, scope), func(t *testing.T) {
				message := "You have insufficient permissions for this operation. Missing scopes: " + scope + ", api.files.write."
				body := `{"error":` + mainPermissionErrorDetails(t, message) + `}`
				result := runMainAPIErrorResponse(t, status, "application/json", body)
				if result.code != 1 || result.stdout != "" {
					t.Fatalf("mixed permission error changed status or output: %+v", result)
				}
				assertMainPermissionGuidance(t, result.stderr, false)
				if strings.Count(result.stderr, "Missing permission: "+scope+".") != 1 {
					t.Errorf("mixed permission error lost webhook access guidance: %q", result.stderr)
				}
			})
		}
	}
}

func TestMainDispatchAPIPermissionBatchPartialCollection(t *testing.T) {
	directory := t.TempDir()
	body := `{"error":` + mainPermissionErrorDetails(t, mainFilesWritePermissionMessage) + `}`
	var retrieved, outputRequests, errorRequests, mutations atomic.Int32
	var corrected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
			http.Error(w, "unexpected mutation", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-should-retry", "false")
		switch r.URL.Path {
		case "/batches/batch_synthetic":
			retrieved.Add(1)
			fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
		case "/files/file_output/content":
			outputRequests.Add(1)
			fmt.Fprint(w, batchesWorkflowContent)
		case "/files/file_error/content":
			errorRequests.Add(1)
			if !corrected.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, body)
				return
			}
			fmt.Fprint(w, "{\"custom_id\":\"synthetic-error\"}\n")
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	result := runReadableCommand(t, server, "batches", "wait", "--batch-id", "batch_synthetic", "--output-dir", directory)
	if result.code != 1 || retrieved.Load() != 1 || outputRequests.Load() != 1 || errorRequests.Load() != 1 || mutations.Load() != 0 {
		t.Fatalf("partial collection changed requests or exit: result=%+v retrieve=%d output=%d error=%d mutations=%d", result, retrieved.Load(), outputRequests.Load(), errorRequests.Load(), mutations.Load())
	}
	assertMainPermissionGuidance(t, result.stderr, false)
	for _, fragment := range []string{"Could not save error.jsonl.", "--file error --output NEW_PATH", "remain saved", "same batch ID and connection options", "do not create another batch"} {
		if !strings.Contains(result.stderr, fragment) {
			t.Errorf("partial collection lost %q: %q", fragment, result.stderr)
		}
	}
	if !strings.Contains(result.stdout, "batch_synthetic") || strings.Contains(result.stdout, "synthetic-private-guidance") {
		t.Errorf("partial collection lost safe batch context: %q", result.stdout)
	}
	outputPath := filepath.Join(directory, "output.jsonl")
	data, err := os.ReadFile(outputPath)
	if err != nil || string(data) != batchesWorkflowContent {
		t.Fatalf("completed output changed: bytes=%q error=%v", data, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "output.jsonl" {
		t.Fatalf("failed download published content or left staging files: entries=%v error=%v", entries, err)
	}
	// Recover only the missing file after access is corrected. Reuse the batch
	// and connection; do not repeat collection or submit a replacement batch.
	corrected.Store(true)
	errorPath := filepath.Join(directory, "error.jsonl")
	recovered := runReadableCommand(t, server, "batches", "download", "--batch-id", "batch_synthetic", "--file", "error", "--output", errorPath)
	if recovered.code != 0 || recovered.stderr != "" || retrieved.Load() != 2 || outputRequests.Load() != 1 || errorRequests.Load() != 2 || mutations.Load() != 0 {
		t.Fatalf("separate download recovery failed: result=%+v retrieve=%d output=%d error=%d mutations=%d", recovered, retrieved.Load(), outputRequests.Load(), errorRequests.Load(), mutations.Load())
	}
	if data, err := os.ReadFile(errorPath); err != nil || string(data) != "{\"custom_id\":\"synthetic-error\"}\n" {
		t.Errorf("recovered error file changed: bytes=%q error=%v", data, err)
	}
	if data, err := os.ReadFile(outputPath); err != nil || string(data) != batchesWorkflowContent {
		t.Errorf("recovery changed the completed output: bytes=%q error=%v", data, err)
	}
}
