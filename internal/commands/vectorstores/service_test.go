package vectorstores

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testService(t *testing.T, handler http.HandlerFunc) Service {
	t.Helper()
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewService(openai.NewClient(
		option.WithBaseURL(server.URL+"/"),
		option.WithHTTPClient(server.Client()),
		option.WithAPIKey("test-key"),
		option.WithOrganization("org_test"),
		option.WithProject("proj_test"),
		option.WithHeader("X-Workspace-Test", "inherited"),
		option.WithMaxRetries(2),
	))
}

func serviceJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestServiceStorePaginationAndContext(t *testing.T) {
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/vector_stores", r.URL.Path)
		assert.Equal(t, "org_test", r.Header.Get("OpenAI-Organization"))
		assert.Equal(t, "proj_test", r.Header.Get("OpenAI-Project"))
		assert.Equal(t, "inherited", r.Header.Get("X-Workspace-Test"))
		assert.Equal(t, "20", r.URL.Query().Get("limit"))
		assert.Equal(t, "desc", r.URL.Query().Get("order"))
		switch r.URL.Query().Get("after") {
		case "":
			serviceJSON(w, 200, `{"data":[{"id":"vs_first","name":"First"},{"id":"vs_second","name":"Second"}],"has_more":true}`)
		case "vs_second":
			serviceJSON(w, 200, `{"data":[{"id":"vs_last","name":"Last"}],"has_more":false}`)
		default:
			serviceJSON(w, 400, `{"error":{"message":"synthetic cursor failure","type":"invalid_request_error"}}`)
		}
	})
	first, err := service.ListStores(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, first.Stores, 2)
	assert.Equal(t, "vs_second", first.Next)
	second, err := service.ListStores(t.Context(), first.Next)
	require.NoError(t, err)
	require.Len(t, second.Stores, 1)
	assert.Empty(t, second.Next)
	_, err = service.ListStores(t.Context(), "invalid")
	var apiError *openai.Error
	require.ErrorAs(t, err, &apiError)
	assert.Equal(t, 400, apiError.StatusCode)
	assert.Equal(t, "vs_first", first.Stores[0].ID)
}

func TestServiceFilePagination(t *testing.T) {
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/vector_stores/vs_owned/files", r.URL.Path)
		assert.Equal(t, "20", r.URL.Query().Get("limit"))
		switch r.URL.Query().Get("after") {
		case "":
			serviceJSON(w, 200, `{"data":[{"id":"file_first","vector_store_id":"vs_owned","status":"in_progress"}],"has_more":true}`)
		case "file_first":
			serviceJSON(w, 200, `{"data":[{"id":"file_last","vector_store_id":"vs_owned","status":"failed","last_error":{"code":"invalid_file"}}],"has_more":false}`)
		default:
			t.Errorf("unexpected cursor")
			serviceJSON(w, 400, `{"error":{"message":"unexpected cursor"}}`)
		}
	})
	first, err := service.ListFiles(t.Context(), "vs_owned", "")
	require.NoError(t, err)
	assert.Equal(t, "file_first", first.Next)
	last, err := service.ListFiles(t.Context(), "vs_owned", first.Next)
	require.NoError(t, err)
	require.Len(t, last.Files, 1)
	assert.Equal(t, openai.VectorStoreFileStatusFailed, last.Files[0].Status)
	assert.Equal(t, "invalid_file", last.Files[0].LastError.Code)
	assert.Empty(t, last.Next)
}

func TestServiceInvalidListResponses(t *testing.T) {
	for _, test := range []struct {
		name, response, after string
	}{
		{"null response", `null`, ""},
		{"missing data", `{"has_more":false}`, ""},
		{"missing completion", `{"data":[]}`, ""},
		{"missing ID", `{"data":[{"name":"Unknown"}],"has_more":true}`, ""},
		{"empty continued page", `{"data":[],"has_more":true}`, ""},
		{"repeated cursor", `{"data":[{"id":"repeated"}],"has_more":true}`, "repeated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := testService(t, func(w http.ResponseWriter, _ *http.Request) {
				serviceJSON(w, 200, test.response)
			})
			stores, err := service.ListStores(t.Context(), test.after)
			require.Error(t, err)
			assert.Empty(t, stores.Next)
			files, err := service.ListFiles(t.Context(), "vs_owned", test.after)
			require.Error(t, err)
			assert.Empty(t, files.Next)
		})
	}
}

func TestServiceCreateLiteralNameAndNoAutomaticRetry(t *testing.T) {
	const name = "@literal $(not-a-command)\nstore"
	var requests atomic.Int32
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/vector_stores", r.URL.Path)
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]any{"name": name}, body)
		serviceJSON(w, 500, `{"error":{"message":"synthetic uncertain result","type":"server_error"}}`)
	})
	_, err := service.CreateStore(t.Context(), name)
	require.Error(t, err)
	assert.Equal(t, int32(1), requests.Load())
}

func TestServiceUploadAttachmentFailureKeepsFile(t *testing.T) {
	payload := []byte("synthetic retrieval text\n")
	path := filepath.Join(t.TempDir(), "synthetic notes.txt")
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	var uploads, attachments atomic.Int32
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			uploads.Add(1)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Positive(t, r.ContentLength)
			if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
				serviceJSON(w, 400, `{"error":{"message":"invalid multipart"}}`)
				return
			}
			defer r.MultipartForm.RemoveAll()
			assert.Equal(t, "assistants", r.FormValue("purpose"))
			source, header, err := r.FormFile("file")
			if !assert.NoError(t, err) {
				serviceJSON(w, 400, `{"error":{"message":"missing file"}}`)
				return
			}
			defer source.Close()
			assert.Equal(t, filepath.Base(path), header.Filename)
			data, err := io.ReadAll(source)
			assert.NoError(t, err)
			assert.Equal(t, payload, data)
			serviceJSON(w, 200, `{"id":"file_uploaded","object":"file","filename":"synthetic notes.txt","purpose":"assistants","status":"processed"}`)
		case "/vector_stores/vs_owned/files":
			n := attachments.Add(1)
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]any{"file_id": "file_uploaded"}, body)
			if n == 1 {
				serviceJSON(w, 500, `{"error":{"message":"synthetic attachment failure","type":"server_error"}}`)
				return
			}
			serviceJSON(w, 200, `{"id":"file_uploaded","vector_store_id":"vs_owned","status":"in_progress"}`)
		default:
			t.Errorf("unexpected request path")
			serviceJSON(w, 404, `{"error":{"message":"unexpected path"}}`)
		}
	})
	file, err := service.UploadFile(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, "file_uploaded", file.ID)
	_, err = service.AttachFile(t.Context(), "vs_owned", file.ID)
	require.Error(t, err)
	assert.Equal(t, int32(1), uploads.Load())
	assert.Equal(t, int32(1), attachments.Load())
	attached, err := service.AttachFile(t.Context(), "vs_owned", file.ID)
	require.NoError(t, err)
	assert.Equal(t, openai.VectorStoreFileStatusInProgress, attached.Status)
	assert.Equal(t, int32(1), uploads.Load(), "attachment retry must not upload again")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, payload, data, "the source file remains unchanged")
}

func TestServiceUploadFailureDoesNotRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic payload"), 0o600))
	var requests atomic.Int32
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		serviceJSON(w, 500, `{"error":{"message":"synthetic upload failure","type":"server_error"}}`)
	})
	_, err := service.UploadFile(t.Context(), path)
	require.Error(t, err)
	assert.Equal(t, int32(1), requests.Load())
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestServiceUploadPathErrorsAndCancellation(t *testing.T) {
	var requests atomic.Int32
	service := testService(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		serviceJSON(w, 500, `{"error":{"message":"unexpected request"}}`)
	})
	privatePath := filepath.Join(t.TempDir(), "missing-private-file.txt")
	_, err := service.UploadFile(t.Context(), privatePath)
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.NotContains(t, err.Error(), privatePath)
	_, err = service.UploadFile(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "regular file")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = service.UploadFile(ctx, privatePath)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, requests.Load())
}

func TestServiceSearchLiteralQueryAndContinuation(t *testing.T) {
	const query = "@not-a-file $(not-a-command)\nsearch"
	for _, test := range []struct {
		name, continuation string
		more               bool
	}{
		{"complete", `"has_more":false,"next_page":null`, false},
		{"more flag", `"has_more":true,"next_page":"opaque"`, true},
		{"token only", `"next_page":"opaque"`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			service := testService(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, "/vector_stores/vs_owned/search", r.URL.Path)
				var body map[string]any
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Equal(t, map[string]any{"query": query, "max_num_results": float64(10)}, body)
				serviceJSON(w, 200, `{"object":"vector_store.search_results.page","data":[`+
					`{"file_id":"file_first","filename":"first.txt","score":0.25,"attributes":{"revision":0},"content":[{"type":"text","text":"first chunk"},{"type":"text","text":"second chunk"}]},`+
					`{"file_id":"file_second","filename":"second.txt","score":0.95,"content":[]}],`+test.continuation+`}`)
			})
			page, err := service.Search(t.Context(), "vs_owned", query)
			require.NoError(t, err)
			require.Len(t, page.Results, 2)
			assert.Equal(t, "file_first", page.Results[0].FileID)
			assert.Equal(t, "first chunk", page.Results[0].Content[0].Text)
			assert.Equal(t, "second chunk", page.Results[0].Content[1].Text)
			assert.Equal(t, 0.25, page.Results[0].Score)
			assert.Equal(t, test.more, page.More)
			assert.Equal(t, int32(1), requests.Load(), "the API has no supported continuation input")
		})
	}
}

func TestServiceEmptySearchAndInvalidResponses(t *testing.T) {
	for _, response := range []string{
		`{"object":"vector_store.search_results.page","data":[],"has_more":false,"next_page":null}`,
		`null`, `{}`,
	} {
		t.Run(response, func(t *testing.T) {
			service := testService(t, func(w http.ResponseWriter, _ *http.Request) {
				serviceJSON(w, 200, response)
			})
			page, err := service.Search(t.Context(), "vs_owned", "synthetic query")
			if strings.Contains(response, `"data":[]`) {
				require.NoError(t, err)
				assert.Empty(t, page.Results)
				assert.False(t, page.More)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestServiceGetStatusAndResponseIdentity(t *testing.T) {
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vector_stores/vs_owned":
			serviceJSON(w, 200, `{"id":"vs_owned","status":"completed"}`)
		case "/vector_stores/vs_owned/files/file_owned":
			serviceJSON(w, 200, `{"id":"file_owned","vector_store_id":"vs_owned","status":"failed","last_error":{"code":"invalid_file","message":"synthetic failure"}}`)
		default:
			serviceJSON(w, 200, `{"id":"different","vector_store_id":"different"}`)
		}
	})
	store, err := service.GetStore(t.Context(), "vs_owned")
	require.NoError(t, err)
	assert.Equal(t, openai.VectorStoreStatusCompleted, store.Status)
	file, err := service.GetFile(t.Context(), "vs_owned", "file_owned")
	require.NoError(t, err)
	assert.Equal(t, openai.VectorStoreFileStatusFailed, file.Status)
	assert.Equal(t, "invalid_file", file.LastError.Code)
	_, err = service.GetStore(t.Context(), "vs_wrong")
	require.Error(t, err)
	_, err = service.GetFile(t.Context(), "vs_owned", "file_wrong")
	require.Error(t, err)
}

func TestServiceRejectsCoercedMutationIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic text"), 0o600))
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		serviceJSON(w, 200, `{"id":42,"vector_store_id":"vs_demo"}`)
	})
	_, err := s.CreateStore(t.Context(), "demo")
	require.Error(t, err)
	uploaded, err := s.UploadFile(t.Context(), path)
	require.Error(t, err)
	require.Nil(t, uploaded, "the workspace must not retain a coerced file ID")
	_, err = s.(ManagementService).CreateBatch(t.Context(), "vs_demo", []string{"file_demo"}, AttachOptions{})
	require.Error(t, err)
	_, err = s.AttachFile(t.Context(), "vs_demo", "42")
	require.Error(t, err)
}

func TestServiceCancellationStopsInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		defer close(stopped)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			t.Error("request did not observe cancellation")
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := service.GetFile(ctx, "vs_owned", "file_owned")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.True(t, errors.Is(err, context.Canceled))
	case <-time.After(5 * time.Second):
		t.Fatal("request remained blocked")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("server request remained active")
	}
}
