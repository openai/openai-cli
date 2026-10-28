package batches

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
)

func TestBatchSubmissionRecoveryState(t *testing.T) {
	for _, tc := range []struct {
		name, source, failStage, response string
		status                            int
		stage, inputID                    string
		uncertain, rejected, success      bool
		requests                          []string
	}{
		{"local success", "local", "", "", 200, "receipt", "file_uploaded", false, false, true, []string{"/files", "/batches"}},
		{"existing success", "existing", "", "", 200, "receipt", "file_existing", false, false, true, []string{"/batches"}},
		{"upload uncertain", "local", "/files", "", 500, "upload", "", true, false, false, []string{"/files"}},
		{"upload rejected", "local", "/files", "", 403, "upload", "", false, true, false, []string{"/files"}},
		{"upload missing ID", "local", "/files", `{}`, 200, "upload", "", true, false, false, []string{"/files"}},
		{"create uncertain", "local", "/batches", "", 500, "create", "file_uploaded", true, false, false, []string{"/files", "/batches"}},
		{"create rejected", "local", "/batches", "", 403, "create", "file_uploaded", false, true, false, []string{"/files", "/batches"}},
		{"create missing ID", "existing", "/batches", `{}`, 200, "create", "file_existing", true, false, false, []string{"/batches"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "requests.jsonl")
			const payload = "{\"custom_id\":\"synthetic\"}\n"
			if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			var requests []string
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.URL.Path)
				if r.Header.Get("OpenAI-Project") != "proj_synthetic" || r.Header.Get("X-Synthetic") != "preserved" {
					t.Error("lost project or literal header")
				}
				if r.URL.Path == "/files" {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer r.MultipartForm.RemoveAll()
					file, _, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						return
					}
					data, err := io.ReadAll(file)
					file.Close()
					if err != nil || string(data) != payload || r.FormValue("purpose") != "batch" {
						t.Error("upload changed")
					}
				} else {
					data, _ := io.ReadAll(r.Body)
					body := gjson.ParseBytes(data)
					for key, want := range map[string]string{"input_file_id": tc.inputID, "endpoint": "/v1/responses", "completion_window": "24h", "metadata.scenario": "synthetic", "output_expires_after.seconds": "86400", "future.exact": "9007199254740993"} {
						if body.Get(key).String() != want {
							t.Errorf("%s changed: %s", key, body.Get(key).String())
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == tc.failStage {
					w.WriteHeader(tc.status)
					if tc.response != "" {
						io.WriteString(w, tc.response)
					} else {
						io.WriteString(w, `{"error":{"message":"synthetic error","type":"synthetic_error"}}`)
					}
					return
				}
				if r.URL.Path == "/files" {
					io.WriteString(w, `{"id":"file_uploaded"}`)
				} else {
					io.WriteString(w, `{"id":"batch_created","status":"validating"}`)
				}
			}))
			defer server.Close()
			client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-key"), option.WithMaxRetries(3))
			options := []option.RequestOption{
				option.WithRequestBody("application/json", []byte(`{"endpoint":"/v1/responses","completion_window":"24h","metadata":{"scenario":"synthetic"},"output_expires_after":{"anchor":"created_at","seconds":86400},"future":{"exact":9007199254740993}}`)),
				option.WithHeader("OpenAI-Project", "proj_synthetic"), option.WithHeader("X-Synthetic", "preserved"),
			}
			inputID := ""
			var reviewed os.FileInfo
			if tc.name == "local success" {
				var err error
				reviewed, err = os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.source == "existing" {
				path, inputID = "", "file_existing"
			}
			state, err := submitBatchRequest(context.Background(), client, options, http.Header{"X-Synthetic": {"preserved"}}, path, inputID, reviewed)
			if (err == nil) != tc.success || state.Stage != tc.stage || state.InputFileID != tc.inputID || state.Uncertain != tc.uncertain || state.Rejected != tc.rejected {
				t.Fatalf("state=%+v err=%v", state, err)
			}
			if tc.success && state.batchID() != "batch_created" {
				t.Fatalf("created identity lost: %+v", state)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(requests, tc.requests) {
				t.Fatalf("unexpected mutations or retries: %v", requests)
			}
		})
	}
}

func TestBatchSubmissionPreparationDoesNotMutate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid source or canceled context caused a mutation")
		w.WriteHeader(500)
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-key"))
	for _, tc := range []struct {
		name, path, id string
		canceled       bool
	}{
		{"neither source", "", "", false},
		{"both sources", "requests.jsonl", "file_existing", false},
		{"canceled", "", "file_existing", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			state, err := submitBatchRequest(ctx, client, nil, nil, tc.path, tc.id, nil)
			if err == nil || state.Stage != "preparation" || state.Uncertain || state.Rejected || state.Batch != nil {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		})
	}
}

func TestBatchSubmissionRejectsChangedReviewedFile(t *testing.T) {
	for _, change := range []string{"replacement", "size", "time"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "requests.jsonl")
			if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reviewed, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "replacement":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				err = os.Chtimes(path, reviewed.ModTime(), reviewed.ModTime())
			case "size":
				err = os.WriteFile(path, []byte("{}\n{}\n"), 0600)
			case "time":
				stamp := reviewed.ModTime().Add(time.Second)
				err = os.Chtimes(path, stamp, stamp)
			}
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("changed file caused a mutation")
				w.WriteHeader(500)
			}))
			defer server.Close()
			client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-key"))
			state, err := submitBatchRequest(context.Background(), client, nil, nil, path, "", reviewed)
			if err == nil || !strings.Contains(err.Error(), "changed after review") || state.Stage != "preparation" || state.Uncertain {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		})
	}
}
