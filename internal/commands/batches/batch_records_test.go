package batches

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func batchRecordsTestCommand(t *testing.T, server string, out io.Writer) *cli.Command {
	t.Helper()
	root := batchDownloadTestCommand(t, server, out)
	root.Commands[0].Commands = []*cli.Command{(workflow{options: testOptions()}).newBatchResultsCommand()}
	return root
}

func TestBatchResultsFileSelectionAndRequestOptions(t *testing.T) {
	for _, selected := range []string{"input", "output", "error"} {
		t.Run(selected, func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "Bearer synthetic-batch-key", r.Header.Get("Authorization"))
				require.Equal(t, "project_records", r.Header.Get("OpenAI-Project"))
				require.Equal(t, "test-value", r.Header.Get("X-Record-Test"))
				if r.URL.Path == "/batches/batch_example" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_example","input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error"}`)
					return
				}
				_, _ = io.WriteString(w, "{\"custom_id\":\"second\",\"extra\":9007199254740993}\r\n\n{\"custom_id\":\"first\",\"text\":\"你好\"}")
			}))
			defer server.Close()
			var out bytes.Buffer
			root := batchRecordsTestCommand(t, server.URL, &out)
			err := root.Run(t.Context(), []string{"openai", "--project", "project_records", "--header", "X-Record-Test: test-value", "batches", "results", "batch_example", "--file", selected})
			require.NoError(t, err)
			require.Equal(t, []string{"/batches/batch_example", "/files/file_" + selected + "/content"}, paths)
			value := gjson.ParseBytes(out.Bytes())
			require.Equal(t, selected, value.Get("file").String())
			require.Equal(t, "file_"+selected, value.Get("file_id").String())
			require.Equal(t, "second", value.Get("data.0.custom_id").String())
			require.Equal(t, "9007199254740993", value.Get("data.0.extra").Raw)
			require.Equal(t, "你好", value.Get("data.1.text").String())
		})
	}
}

func TestBatchResultsMatchByIDAndExposeAmbiguity(t *testing.T) {
	inputs := strings.Join([]string{
		`{"custom_id":"a","body":{"input":"input a"}}`,
		`{"custom_id":"b","body":{"input":"input b"}}`,
		`{"custom_id":"duplicate","body":{"input":"first duplicate"}}`,
		`{"custom_id":"duplicate","body":{"input":"second duplicate"}}`,
		`{"custom_id":"unfinished","body":{"input":"no returned record"}}`,
		`{"body":{"input":"missing ID"}}`,
	}, "\n")
	results := strings.Join([]string{
		`{"custom_id":"b","response":{"body":"result b"}}`,
		`{"custom_id":"a","response":{"body":"result a"}}`,
		`{"custom_id":"duplicate","response":{"body":"first result"}}`,
		`{"custom_id":"duplicate","response":{"body":"second result"}}`,
		`{"custom_id":"unknown","error":{"code":"synthetic"}}`,
		`{"custom_id":null,"error":{"code":"missing ID"}}`,
	}, "\n")
	for _, selected := range []string{"output", "error"} {
		t.Run(selected, func(t *testing.T) {
			for _, filter := range []string{"", "duplicate", "unfinished", "absent"} {
				t.Run("filter_"+filter, func(t *testing.T) {
					var paths []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						paths = append(paths, r.URL.Path)
						switch r.URL.Path {
						case "/batches/batch_example":
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, `{"id":"batch_example","input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error"}`)
						case "/files/file_input/content":
							_, _ = io.WriteString(w, inputs)
						case "/files/file_" + selected + "/content":
							_, _ = io.WriteString(w, results)
						default:
							http.NotFound(w, r)
						}
					}))
					defer server.Close()
					var out bytes.Buffer
					root := batchRecordsTestCommand(t, server.URL, &out)
					args := []string{"openai", "batches", "results", "--batch-id", "batch_example", "--file", selected, "--match-input"}
					if filter != "" {
						args = append(args, "--custom-id", filter)
					}
					require.NoError(t, root.Run(t.Context(), args))
					require.Equal(t, []string{"/batches/batch_example", "/files/file_" + selected + "/content", "/files/file_input/content"}, paths)
					data := gjson.ParseBytes(out.Bytes()).Get("data")
					switch filter {
					case "duplicate":
						require.Len(t, data.Array(), 1)
						require.Equal(t, "ambiguous", data.Get("0.match").String())
					case "unfinished":
						require.Len(t, data.Array(), 1)
						require.Equal(t, "missing_result", data.Get("0.match").String())
					case "absent":
						require.Equal(t, "[]", data.Raw)
					default:
						require.Len(t, data.Array(), 7)
						require.Equal(t, "input b", data.Get("0.inputs.0.body.input").String())
						require.Equal(t, "result b", data.Get("0.results.0.response.body").String())
						require.Equal(t, "matched", data.Get("0.match").String())
						require.Equal(t, "input a", data.Get("1.inputs.0.body.input").String())
						require.Equal(t, "ambiguous", data.Get("2.match").String())
						require.Len(t, data.Get("2.inputs").Array(), 2)
						require.Len(t, data.Get("2.results").Array(), 2)
						require.Equal(t, "duplicate_input_custom_id", data.Get("2.issues.0").String())
						require.Equal(t, "duplicate_result_custom_id", data.Get("2.issues.1").String())
						require.Equal(t, "missing_input", data.Get("3.match").String())
						require.Equal(t, "[]", data.Get("3.inputs").Raw)
						require.Equal(t, "missing_custom_id", data.Get("4.match").String())
						require.Equal(t, "missing_result", data.Get("5.match").String())
						require.Equal(t, "[]", data.Get("5.results").Raw)
						require.Equal(t, "missing_input_custom_id", data.Get("6.issues.0").String())
					}
				})
			}
		})
	}
}

func TestBatchResultsRejectMalformedJSONLWithoutPayloadDiagnostics(t *testing.T) {
	for name, payload := range map[string]string{
		"malformed":       "{\"custom_id\":\"safe\"}\n{\"private-payload\":",
		"array":           `[{"private-payload":true}]`,
		"scalar":          `"private-payload"`,
		"null":            `null`,
		"multiple_values": `{"private-payload":true} {}`,
		"multiline":       "{\n\"private-payload\":true\n}",
		"duplicate_field": `{"custom_id":"private-payload","\u0063ustom_id":"other"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/batches/batch_example" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
					return
				}
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()
			var out bytes.Buffer
			root := batchRecordsTestCommand(t, server.URL, &out)
			err := root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example"})
			require.Error(t, err)
			require.Contains(t, err.Error(), "line")
			require.Contains(t, err.Error(), "Download the file")
			require.NotContains(t, err.Error(), "private-payload")
			if name == "malformed" {
				require.Contains(t, out.String(), `"custom_id":"safe"`)
				require.NotContains(t, out.String(), "private-payload")
				require.Contains(t, err.Error(), "Output may be incomplete")
			} else {
				require.Empty(t, out.String())
			}
		})
	}
}

func TestBatchResultsLargeRecordFilteringAndExtraction(t *testing.T) {
	large := strings.Repeat("x", 4*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_example" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		_, _ = fmt.Fprintf(w, "{\"custom_id\":\"large\",\"response\":{\"body\":\"%s\"}}\n{\"custom_id\":\"other\"}", large)
	}))
	defer server.Close()
	for _, extract := range []bool{false, true} {
		t.Run(fmt.Sprint(extract), func(t *testing.T) {
			var out bytes.Buffer
			root := batchRecordsTestCommand(t, server.URL, &out)
			args := []string{"openai", "batches", "results", "batch_example", "--custom-id", "large"}
			if extract {
				args = append(args, "--transform", "data.0.response.body", "--raw-output")
			}
			require.NoError(t, root.Run(t.Context(), args))
			if extract {
				require.Equal(t, large+"\n", out.String())
			} else {
				data := gjson.ParseBytes(out.Bytes()).Get("data")
				require.Len(t, data.Array(), 1)
				require.Equal(t, large, data.Get("0.response.body").String())
			}
		})
	}
}

func TestBatchResultsCancellationStopsFileRead(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_example" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		_, _ = io.WriteString(w, `{"custom_id":"blocked","response":"`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out bytes.Buffer
	root := batchRecordsTestCommand(t, server.URL, &out)
	done := make(chan error, 1)
	go func() { done <- root.Run(ctx, []string{"openai", "batches", "results", "batch_example"}) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("file request did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		var workflowErr *WorkflowError
		require.ErrorAs(t, err, &workflowErr)
		require.Equal(t, 130, workflowErr.ExitCode())
	case <-time.After(3 * time.Second):
		t.Fatal("canceled file request did not stop")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("file response did not close")
	}
	require.Empty(t, out.String())
}

func TestBatchResultsValidationAndMissingFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		batch string
	}{
		{"invalid_file", []string{"--file", "wrong"}, ""},
		{"input_matching", []string{"--file", "input", "--match-input"}, ""},
		{"empty_id", []string{"--custom-id", ""}, ""},
		{"extra_argument", []string{"second_batch"}, ""},
		{"missing_output", nil, `{"id":"batch_example"}`},
		{"missing_input", []string{"--match-input"}, `{"id":"batch_example","output_file_id":"file_output"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "/batches/batch_example", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.batch)
			}))
			defer server.Close()
			var out bytes.Buffer
			root := batchRecordsTestCommand(t, server.URL, &out)
			args := append([]string{"openai", "batches", "results", "batch_example"}, tc.args...)
			require.Error(t, root.Run(t.Context(), args))
			if tc.batch == "" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			require.Empty(t, out.String())
		})
	}
}

func TestBatchResultsPreservesAPIErrorsAndIncompleteRead(t *testing.T) {
	for _, failure := range []string{"batch", "file", "incomplete"} {
		t.Run(failure, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				batchRequest := r.URL.Path == "/batches/batch_example"
				if failure == "batch" || (failure == "file" && !batchRequest) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic file read denied","type":"permission_error"}}`)
					return
				}
				if batchRequest {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
					return
				}
				w.Header().Set("Content-Length", "1000")
				_, _ = io.WriteString(w, "{\"custom_id\":\"first\"}\n")
			}))
			defer server.Close()
			var out bytes.Buffer
			root := batchRecordsTestCommand(t, server.URL, &out)
			err := root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example"})
			require.Error(t, err)
			if failure == "incomplete" {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				require.Contains(t, err.Error(), "Retry results")
				require.Contains(t, out.String(), `"custom_id":"first"`)
				require.Contains(t, err.Error(), "Output may be incomplete")
			} else {
				var apiErr *openai.Error
				require.True(t, errors.As(err, &apiErr))
				require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
				require.Empty(t, out.String())
			}
		})
	}
}

func TestBatchResultsCustomIDUsesExactFieldName(t *testing.T) {
	for _, filter := range []string{"", "correct", "wrong"} {
		t.Run("filter_"+filter, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/batches/batch_example":
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_example","input_file_id":"file_input","output_file_id":"file_output"}`)
				case "/files/file_input/content":
					_, _ = io.WriteString(w, `{"custom_id":"correct","body":{"input":"correct input"}}`)
				default:
					_, _ = io.WriteString(w, "{\"custom_id\":\"correct\",\"CUSTOM_ID\":\"wrong\"}\n{\"CUSTOM_ID\":\"correct\"}")
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			root := batchRecordsTestCommand(t, server.URL, &out)
			args := []string{"openai", "batches", "results", "batch_example", "--match-input"}
			if filter != "" {
				args = append(args, "--custom-id", filter)
			}
			require.NoError(t, root.Run(t.Context(), args))
			data := gjson.ParseBytes(out.Bytes()).Get("data")
			if filter == "wrong" {
				require.Empty(t, data.Array())
				return
			}
			require.Equal(t, "correct", data.Get("0.custom_id").String())
			require.Equal(t, "matched", data.Get("0.match").String())
			require.Equal(t, "correct input", data.Get("0.inputs.0.body.input").String())
			require.Equal(t, "wrong", data.Get("0.results.0.CUSTOM_ID").String())
			if filter == "" {
				require.Len(t, data.Array(), 2)
				require.Equal(t, "missing_custom_id", data.Get("1.match").String())
				require.Empty(t, data.Get("1.inputs").Array())
			} else {
				require.Len(t, data.Array(), 1)
			}
		})
	}
}

func TestBatchResultsGroupsDuplicateIDsWithoutMultiplyingRecords(t *testing.T) {
	const count = 100
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_example" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","input_file_id":"file_input","output_file_id":"file_output"}`)
			return
		}
		for i := range count {
			_, _ = fmt.Fprintf(w, "{\"custom_id\":\"duplicate\",\"sequence\":%d}\n", i)
		}
	}))
	defer server.Close()
	var out bytes.Buffer
	root := batchRecordsTestCommand(t, server.URL, &out)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example", "--match-input"}))
	data := gjson.ParseBytes(out.Bytes()).Get("data")
	require.Len(t, data.Array(), 1)
	require.Equal(t, "ambiguous", data.Get("0.match").String())
	require.Len(t, data.Get("0.inputs").Array(), count)
	require.Len(t, data.Get("0.results").Array(), count)
	require.Equal(t, int64(count-1), data.Get("0.results.99.sequence").Int())
}
