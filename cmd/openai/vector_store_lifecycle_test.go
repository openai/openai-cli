package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cases verify public command contracts against synthetic responses.
// They do not establish live indexing, applied chunking, or deletion consistency.
func TestMainVectorStoreLifecycle(t *testing.T) {
	const storePath = "/vector_stores/vs_owned"
	const filePath = storePath + "/files/file_owned"
	const batchPath = storePath + "/file_batches/batch_owned"
	const expiration = `{"anchor":"last_active_at","days":7}`
	const attributes = `{"category":"guides","revision":0,"active":false}`
	const chunking = `{"type":"static","static":{"max_chunk_size_tokens":1200,"chunk_overlap_tokens":200}}`
	const metadata = `{"category":"guides","empty":""}`
	const store = `{"id":"vs_owned","object":"vector_store","name":"Synthetic guides","status":"completed","created_at":1700000000,"last_active_at":1700000100,"usage_bytes":1234,"file_counts":{"in_progress":0,"completed":1,"failed":0,"cancelled":0,"total":1},"expires_after":` + expiration + `,"expires_at":1700604900,"metadata":` + metadata + `,"future_field":{"retained":true}}`
	const file = `{"id":"file_owned","object":"vector_store.file","vector_store_id":"vs_owned","status":"completed","attributes":` + attributes + `,"chunking_strategy":` + chunking + `}`
	const failedFile = `{"id":"file_owned","object":"vector_store.file","vector_store_id":"vs_owned","status":"failed","last_error":{"code":"unsupported_file","message":"synthetic unsupported document"}}`
	const batch = `{"id":"batch_owned","object":"vector_store.files_batch","vector_store_id":"vs_owned","status":"in_progress","file_counts":{"in_progress":1,"completed":1,"failed":0,"cancelled":0,"total":2}}`
	for _, tc := range []struct {
		name, method, path, query, body, input, response, want string
		format                                                 string
		args                                                   []string
		status                                                 int
	}{
		{
			name: "store create expiration", method: http.MethodPost, path: "/vector_stores",
			args: []string{"vector-stores", "create", "--name", "Synthetic guides", "--expires-after.anchor", "last_active_at", "--expires-after.days", "7"},
			body: `{"name":"Synthetic guides","expires_after":` + expiration + `}`, response: store,
		},
		{
			name: "store retrieve expiration", method: http.MethodGet, path: storePath,
			args: []string{"vector-stores", "retrieve", "--vector-store-id", "vs_owned"}, response: store,
		},
		{
			name: "store retrieve raw details", method: http.MethodGet, path: storePath, format: "raw",
			args: []string{"vector-stores", "retrieve", "--vector-store-id", "vs_owned"}, response: store,
		},
		{
			name: "store extract metadata", method: http.MethodGet, path: storePath,
			args: []string{"vector-stores", "retrieve", "--vector-store-id", "vs_owned", "--transform", "metadata"}, response: store, want: metadata,
		},
		{
			name: "store rename preserves input metadata", method: http.MethodPost, path: storePath,
			args:  []string{"vector-stores", "update", "--vector-store-id", "vs_owned", "--name", "Synthetic guides"},
			input: `{"name":"Previous name","metadata":` + metadata + `}`,
			body:  `{"name":"Synthetic guides","metadata":` + metadata + `}`, response: store,
		},
		{
			name: "store update metadata", method: http.MethodPost, path: storePath,
			args: []string{"vector-stores", "update", "--vector-store-id", "vs_owned", "--metadata", metadata},
			body: `{"metadata":` + metadata + `}`, response: store,
		},
		{
			name: "store update expiration", method: http.MethodPost, path: storePath,
			args: []string{"vector-stores", "update", "--vector-store-id", "vs_owned", "--expires-after", expiration},
			body: `{"expires_after":` + expiration + `}`, response: store,
		},
		{
			name: "store clear expiration flag", method: http.MethodPost, path: storePath,
			args: []string{"vector-stores", "update", "--vector-store-id", "vs_owned", "--expires-after", "null"},
			body: `{"expires_after":null}`, response: `{"id":"vs_owned","object":"vector_store","expires_after":null,"expires_at":null}`,
		},
		{
			name: "store clear expiration JSON", method: http.MethodPost, path: storePath,
			args:  []string{"vector-stores", "update", "--vector-store-id", "vs_owned"},
			input: `{"expires_after":null}`, body: `{"expires_after":null}`,
			response: `{"id":"vs_owned","object":"vector_store","expires_after":null,"expires_at":null}`,
		},
		{
			name: "store clear expiration YAML", method: http.MethodPost, path: storePath,
			args:  []string{"vector-stores", "update", "--vector-store-id", "vs_owned"},
			input: "expires_after: null\n", body: `{"expires_after":null}`,
			response: `{"id":"vs_owned","object":"vector_store","expires_after":null,"expires_at":null}`,
		},
		{
			name: "file attach static chunking", method: http.MethodPost, path: storePath + "/files",
			args: []string{"vector-stores:files", "create", "--vector-store-id", "vs_owned", "--file-id", "file_owned", "--attributes", attributes, "--chunking-strategy", chunking},
			body: `{"file_id":"file_owned","attributes":` + attributes + `,"chunking_strategy":` + chunking + `}`, response: file,
		},
		{
			name: "file retrieve configuration", method: http.MethodGet, path: filePath,
			args: []string{"vector-stores", "files", "retrieve", "--vector-store-id", "vs_owned", "--file-id", "file_owned"}, response: file,
		},
		{
			name: "file update typed attributes", method: http.MethodPost, path: filePath,
			args: []string{"vector-stores:files", "update", "--vector-store-id", "vs_owned", "--file-id", "file_owned", "--attributes", attributes},
			body: `{"attributes":` + attributes + `}`, response: file,
		},
		{
			name: "file indexing failure remains data", method: http.MethodGet, path: filePath,
			args: []string{"vector-stores:files", "retrieve", "--vector-store-id", "vs_owned", "--file-id", "file_owned"}, response: failedFile,
		},
		{
			name: "file cancelled status remains data", method: http.MethodGet, path: filePath,
			args:     []string{"vector-stores:files", "retrieve", "--vector-store-id", "vs_owned", "--file-id", "file_owned"},
			response: `{"id":"file_owned","object":"vector_store.file","vector_store_id":"vs_owned","status":"cancelled","last_error":null}`,
		},
		{
			name: "file status request failure", method: http.MethodGet, path: filePath, status: http.StatusServiceUnavailable,
			args:     []string{"vector-stores:files", "retrieve", "--vector-store-id", "vs_owned", "--file-id", "file_owned"},
			response: `{"error":{"type":"server_error","message":"synthetic status unavailable"}}`,
		},
		{
			name: "file list status and cursor", method: http.MethodGet, path: storePath + "/files",
			args:     []string{"vector-stores:files", "list", "--vector-store-id", "vs_owned", "--filter", "failed", "--after", "file_cursor", "--limit", "1", "--order", "asc", "--max-items", "-1"},
			query:    "filter=failed&after=file_cursor&limit=1&order=asc",
			response: `{"object":"list","data":[` + failedFile + `],"first_id":"file_owned","last_id":"file_owned","has_more":false}`,
			want:     `[` + failedFile + `]`,
		},
		{
			name: "file parsed content", method: http.MethodGet, path: filePath + "/content",
			args:     []string{"vector-stores:files", "content", "--vector-store-id", "vs_owned", "--file-id", "file_owned"},
			response: `{"object":"vector_store.file_content.page","data":[{"type":"text","text":"synthetic parsed content\n"}],"has_more":false,"next_page":null}`,
			want:     `[{"type":"text","text":"synthetic parsed content\n"}]`,
		},
		{
			name: "detach does not delete source", method: http.MethodDelete, path: filePath,
			args:     []string{"vector-stores:files", "delete", "--vector-store-id", "vs_owned", "--file-id", "file_owned"},
			response: `{"id":"file_owned","object":"vector_store.file.deleted","deleted":true}`,
		},
		{
			name: "delete owned store only", method: http.MethodDelete, path: storePath,
			args:     []string{"vector-stores", "delete", "--vector-store-id", "vs_owned"},
			response: `{"id":"vs_owned","object":"vector_store.deleted","deleted":true}`,
		},
		{
			name: "delete owned source explicitly", method: http.MethodDelete, path: "/files/file_owned",
			args:     []string{"files", "delete", "--file-id", "file_owned"},
			response: `{"id":"file_owned","object":"file","deleted":true}`,
		},
		{
			name: "batch create shared configuration", method: http.MethodPost, path: storePath + "/file_batches",
			args: []string{"vector-stores:file-batches", "create", "--vector-store-id", "vs_owned", "--file-id", "file_owned", "--file-id", "file_other", "--attributes", attributes, "--chunking-strategy", chunking},
			body: `{"file_ids":["file_owned","file_other"],"attributes":` + attributes + `,"chunking_strategy":` + chunking + `}`, response: batch,
		},
		{
			name: "batch retrieve mixed progress", method: http.MethodGet, path: batchPath,
			args: []string{"vector-stores", "file-batches", "retrieve", "--vector-store-id", "vs_owned", "--batch-id", "batch_owned"}, response: batch,
		},
		{
			name: "batch list failed files", method: http.MethodGet, path: batchPath + "/files",
			args:     []string{"vector-stores:file-batches", "list-files", "--vector-store-id", "vs_owned", "--batch-id", "batch_owned", "--filter", "failed", "--limit", "1", "--order", "asc"},
			query:    "filter=failed&limit=1&order=asc",
			response: `{"object":"list","data":[` + failedFile + `],"first_id":"file_owned","last_id":"file_owned","has_more":false}`,
			want:     `[` + failedFile + `]`,
		},
		{
			name: "batch cancel preserves completed count", method: http.MethodPost, path: batchPath + "/cancel",
			args:     []string{"vector-stores:file-batches", "cancel", "--vector-store-id", "vs_owned", "--batch-id", "batch_owned"},
			response: `{"id":"batch_owned","object":"vector_store.files_batch","vector_store_id":"vs_owned","status":"cancelled","file_counts":{"in_progress":0,"completed":1,"failed":0,"cancelled":1,"total":2}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, tc.method, r.Method)
				assert.Equal(t, tc.path, r.URL.Path)
				query, err := url.ParseQuery(tc.query)
				assert.NoError(t, err)
				assert.Equal(t, query, r.URL.Query())
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				if tc.body == "" {
					assert.Empty(t, body)
				} else {
					assert.JSONEq(t, tc.body, string(body))
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-should-retry", "false")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			var input *os.File
			if tc.input != "" {
				input = shellFileInput(t, []byte(tc.input))
			}
			format := tc.format
			if format == "" {
				format = "json"
			}
			got := runVectorStoreCommand(t, server, input, append([]string{"--format", format}, tc.args...)...)
			require.EqualValues(t, 1, requests.Load(), "one command must not poll or delete additional resources")
			if tc.status != 0 {
				require.Equal(t, 1, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				require.True(t, json.Valid([]byte(got.stderr)), "%q", got.stderr)
				require.Contains(t, got.stderr, "synthetic status unavailable")
				return
			}
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			want := tc.want
			if want == "" {
				want = tc.response
			}
			if format == "raw" {
				require.Equal(t, want+"\n", got.stdout)
			} else {
				require.JSONEq(t, want, got.stdout)
			}
		})
	}
}

func TestMainVectorStoreListPagination(t *testing.T) {
	const first = `{"id":"vs_first","object":"vector_store","name":"First store","created_at":1700000000,"metadata":{"category":"guides"}}`
	const second = `{"id":"vs_second","object":"vector_store","name":"Second store","created_at":1700000100,"metadata":{"category":"notes"},"future_field":true}`
	const firstPage = `{"object":"list","data":[` + first + `],"first_id":"vs_first","last_id":"vs_first","has_more":true}`
	const secondPage = `{"object":"list","data":[` + second + `],"first_id":"vs_second","last_id":"vs_second","has_more":false}`
	for _, tc := range []struct {
		name, format, maximum, want string
		requests                    int32
		failSecond                  bool
	}{
		{"all JSON records", "json", "-1", `[` + first + `,` + second + `]`, 2, false},
		{"bounded JSON records", "json", "1", `[` + first + `]`, 1, false},
		{"raw first page", "raw", "-1", firstPage, 1, false},
		{"later JSON failure", "json", "-1", "", 2, true},
		{"later JSONL failure", "jsonl", "-1", first, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/vector_stores", r.URL.Path)
				query := url.Values{"limit": {"1"}, "order": {"asc"}, "after": {"vs_prior"}}
				if page == 2 {
					query.Set("after", "vs_first")
				}
				assert.Equal(t, query, r.URL.Query())
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Empty(t, body)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-should-retry", "false")
				switch {
				case page == 1:
					_, _ = io.WriteString(w, firstPage)
				case page == 2 && tc.failSecond:
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"error":{"type":"permission_error","message":"synthetic later page denied"}}`)
				case page == 2:
					_, _ = io.WriteString(w, secondPage)
				default:
					t.Errorf("unexpected page request %d", page)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			got := runVectorStoreCommand(t, server, nil, "--format", tc.format,
				"vector-stores", "list", "--after", "vs_prior", "--limit", "1", "--order", "asc", "--max-items", tc.maximum)
			require.Equal(t, tc.requests, requests.Load())
			if tc.failSecond {
				require.Equal(t, 1, got.code, "%+v", got)
				require.True(t, json.Valid([]byte(got.stderr)), "%q", got.stderr)
				require.Contains(t, got.stderr, "synthetic later page denied")
				if tc.format == "json" {
					require.False(t, json.Valid([]byte(got.stdout)), "failed pagination must not claim a complete array")
					require.True(t, strings.HasPrefix(got.stdout, "[\n"), "%q", got.stdout)
					require.JSONEq(t, first, strings.TrimSpace(strings.TrimPrefix(got.stdout, "[\n")))
					return
				}
			} else {
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
			}
			if tc.format == "raw" {
				require.Equal(t, tc.want+"\n", got.stdout)
			} else {
				require.JSONEq(t, tc.want, got.stdout)
			}
		})
	}
}
