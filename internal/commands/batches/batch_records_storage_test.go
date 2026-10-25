package batches

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func batchRecordsPrivateTemp(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, directory)
	}
	return directory
}

func TestBatchResultsCleanupPreservesReplacedScratchFile(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	marker := "synthetic replacement owned by the caller"
	replaced := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_example" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		defer cancel()
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Len(t, entries, 4)
		path := filepath.Join(directory, entries[0].Name())
		require.NoError(t, os.Rename(path, filepath.Join(directory, "original-owned-scratch")))
		require.NoError(t, os.WriteFile(path, []byte(marker), 0o600))
		replaced <- path
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()
	root := batchRecordsTestCommand(t, server.URL, io.Discard)
	err := root.Run(ctx, []string{"openai", "batches", "results", "batch_example"})
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, WorkflowErrorMessage(err), "The replacement was left unchanged.")
	data, err := os.ReadFile(<-replaced)
	require.NoError(t, err)
	require.Equal(t, marker, string(data))
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 2, "only the caller's replacement and relocated original should remain")
}

func TestBatchResultsInvalidUTF8IDsRemainDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/batches/batch_example":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output","input_file_id":"file_input"}`)
		case "/files/file_output/content":
			_, _ = io.WriteString(w, "{\"custom_id\":\"\xff\",\"result\":\"invalid-byte\"}\n")
		case "/files/file_input/content":
			_, _ = io.WriteString(w, "{\"custom_id\":\"\xef\xbf\xbd\",\"input\":\"replacement-rune\"}\n")
		}
	}))
	defer server.Close()
	for _, filter := range []string{"", "\ufffd", "\xff"} {
		var out bytes.Buffer
		root := batchRecordsTestCommand(t, server.URL, &out)
		args := []string{"openai", "batches", "results", "batch_example", "--match-input"}
		if filter != "" {
			args = append(args, "--custom-id", filter)
		}
		require.NoError(t, root.Run(t.Context(), args))
		rows := gjson.GetBytes(out.Bytes(), "data").Array()
		if filter == "" {
			require.Len(t, rows, 2)
			require.Equal(t, "missing_input", rows[0].Get("match").String())
			require.Equal(t, "missing_result", rows[1].Get("match").String())
		} else {
			require.Len(t, rows, 1)
			status := "missing_result"
			if filter == "\xff" {
				status = "missing_input"
			}
			require.Equal(t, status, rows[0].Get("match").String())
		}
	}
}

func TestBatchResultsLargeCustomIDDoesNotAccumulate(t *testing.T) {
	batchRecordsPrivateTemp(t)
	var baseline runtime.MemStats
	var retained atomic.Int64
	chunk := strings.Repeat("x", 32*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_example" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		_, _ = io.WriteString(w, `{"custom_id":"`)
		for range 1536 {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
		w.(http.Flusher).Flush()
		// Keep the string unfinished while sampling. A full-string decoder
		// cannot release an accumulated ID before the following closing quote.
		runtime.GC()
		var current runtime.MemStats
		runtime.ReadMemStats(&current)
		retained.Store(int64(current.HeapAlloc) - int64(baseline.HeapAlloc))
		_, _ = io.WriteString(w, `"}`)
	}))
	defer server.Close()
	root := batchRecordsTestCommand(t, server.URL, io.Discard)
	runtime.GC()
	runtime.ReadMemStats(&baseline)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example", "--custom-id", "absent"}))
	require.Less(t, retained.Load(), int64(16*1024*1024), "custom_id parsing must not retain the 48 MiB string")
}

func TestBatchResultsParserDifferential(t *testing.T) {
	for _, payload := range []string{
		`{"custom_id":"\u0061","n":-0,"x":1.2300e+99}`,
		`{"\u0063ustom_id":"\ud800a\udc00\u2028","n":9007199254740993}`,
		"{\"custom_id\":\"\xff\xed\xa0\x80\",\"x\":1e-1000}",
		`{"a":01}`, `{"a":-01}`, `{"a":--1}`, `{"a":NaN}`, `{"a":1+2}`,
		`{"a":1.e2}`, `{"a":1e+}`, `{"a":1e-01}`, `{"a":nullx}`,
		`{"s":"\u123z"}`, `{"s":"\x20"}`, `{"s":"unterminated}`,
		`{"a":true,}`, `{"a":[false,]}`, `{"a":[{]}`, `{"a":true} trailing`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/batches/batch_example" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			} else {
				_, _ = io.WriteString(w, payload)
			}
		}))
		var out bytes.Buffer
		root := batchRecordsTestCommand(t, server.URL, &out)
		err := root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example"})
		server.Close()
		if json.Valid([]byte(payload)) {
			require.NoError(t, err, "valid standard JSON: %q", payload)
			require.True(t, json.Valid(out.Bytes()))
		} else {
			require.Error(t, err, "invalid standard JSON: %q", payload)
			require.Empty(t, out.String())
		}
	}
}

func TestBatchResultsMatchedIDsPreservePublishedEscapeDecoding(t *testing.T) {
	for _, inputID := range []string{`"\u0061"`, `"\ud800\u2028"`, `"\ud800\ud800\udc00"`, `"\u0000\"\\\n"`} {
		expectedID := gjson.Parse(inputID).Str
		encodedID, err := json.Marshal(expectedID)
		require.NoError(t, err)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/batches/batch_example":
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output","input_file_id":"file_input"}`)
			case "/files/file_input/content":
				_, _ = io.WriteString(w, `{"\u0063ustom_id":`+inputID+`}`)
			case "/files/file_output/content":
				_, _ = io.WriteString(w, `{"custom_id":`+string(encodedID)+`}`)
			}
		}))
		var out bytes.Buffer
		root := batchRecordsTestCommand(t, server.URL, &out)
		err = root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example", "--match-input", "--custom-id", expectedID})
		server.Close()
		require.NoError(t, err)
		var response struct {
			Data []struct {
				CustomID        string `json:"custom_id"`
				MatchStatus     string `json:"match"`
				Inputs, Results []json.RawMessage
			}
		}
		require.NoError(t, json.Unmarshal(out.Bytes(), &response))
		require.Len(t, response.Data, 1)
		require.Equal(t, expectedID, response.Data[0].CustomID)
		require.Equal(t, "matched", response.Data[0].MatchStatus)
		require.Len(t, response.Data[0].Inputs, 1)
		require.Len(t, response.Data[0].Results, 1)
	}
}

type failedBatchRecordWriter struct{ err error }

func (w failedBatchRecordWriter) Write([]byte) (int, error) { return 0, w.err }

type failedBatchRecordReader struct{ err error }

func (r failedBatchRecordReader) Read([]byte) (int, error) { return 0, r.err }

func TestBatchRecordScratchReadFailureHasRecovery(t *testing.T) {
	want := errors.New("synthetic scratch read failure")
	reader := batchRecordContextReader{t.Context(), failedBatchRecordReader{want}}
	var data [32]byte
	_, err := reader.Read(data[:])
	require.ErrorIs(t, err, want)
	require.Contains(t, WorkflowErrorMessage(err), "temporary storage space and permissions")
}

func truncateBatchRecordPayload(t *testing.T, directory, record string, length int64) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		if bytes.HasPrefix(data, []byte(record)) {
			require.NoError(t, os.Truncate(path, length))
			return
		}
	}
	t.Fatal("the synthetic record payload was not found")
}

func TestBatchResultsTruncatedStorageHasRecovery(t *testing.T) {
	const record = `{"custom_id":"request_a","response":{"body":"synthetic"}}`
	for _, length := range []int64{0, 10} {
		t.Run(fmt.Sprintf("length-%d", length), func(t *testing.T) {
			directory := batchRecordsPrivateTemp(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/batches/batch_example" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
					return
				}
				_, _ = io.WriteString(w, record)
			}))
			defer server.Close()
			options := testOptions()
			previous := options.ShowRecords
			options.ShowRecords = func(ctx context.Context, command *cli.Command, records RecordOutput, out, stderr io.Writer) error {
				truncateBatchRecordPayload(t, directory, record, length)
				return previous(ctx, command, records, out, stderr)
			}
			root := batchRecordsTestCommand(t, server.URL, io.Discard)
			root.Commands[0].Commands[0] = (workflow{options: options}).newBatchResultsCommand()
			err := root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example"})
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			message := WorkflowErrorMessage(err)
			require.Contains(t, message, "Temporary batch data is incomplete")
			require.Contains(t, message, "Output may be incomplete")
			require.Contains(t, message, "Retry the same read-only command")
			require.NotContains(t, message, directory)
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestBatchRecordCopyPreservesOutputFailure(t *testing.T) {
	store, err := newBatchRecordStore(t.Context())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.close()) }()
	require.NoError(t, store.readJSONL(strings.NewReader(`{"custom_id":"request_a"}`), "output", "", false, false))
	want := errors.New("synthetic output failure")
	err = store.writeRecord(failedBatchRecordWriter{want}, 1)
	require.ErrorIs(t, err, want)
	require.NotContains(t, err.Error(), "temporary")
	require.NotErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestBatchRecordMidRecordStorageFailureHasRecovery(t *testing.T) {
	want := errors.New("synthetic temporary storage full")
	scanner := batchJSONScanner{ctx: t.Context(), in: bufio.NewReader(strings.NewReader(`{"body":"` + strings.Repeat("x", 10000) + `"}`)), out: bufio.NewWriterSize(failedBatchRecordWriter{want}, 16)}
	err := scanner.value(0)
	require.ErrorIs(t, err, want)
	require.Contains(t, err.Error(), "temporary storage space and permissions")
	require.Contains(t, err.Error(), "retry the same command")
}

func TestBatchResultsLargeRecordUsesTemporaryStorage(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	var baseline runtime.MemStats
	var retained atomic.Int64
	chunk := strings.Repeat("x", 32*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/batches/batch_example":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","input_file_id":"file_input","output_file_id":"file_output"}`)
		case "/files/file_output/content":
			_, _ = io.WriteString(w, `{"custom_id":"large","response":{"body":"`)
			for range 1536 {
				if _, err := io.WriteString(w, chunk); err != nil {
					return
				}
			}
			_, _ = io.WriteString(w, "\"}}\n")
		case "/files/file_input/content":
			// This request starts only after the complete 48 MiB output record
			// has been read. The old implementation retained that record here.
			runtime.GC()
			var current runtime.MemStats
			runtime.ReadMemStats(&current)
			retained.Store(int64(current.HeapAlloc) - int64(baseline.HeapAlloc))
			_, _ = io.WriteString(w, `{"custom_id":"large","body":{"input":"synthetic"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	root := batchRecordsTestCommand(t, server.URL, io.Discard)
	runtime.GC()
	runtime.ReadMemStats(&baseline)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example", "--match-input"}))
	require.Less(t, retained.Load(), int64(16*1024*1024), "retained payload memory must not scale with the 48 MiB record")
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries, "the command must remove its private scratch directory")
}

func TestBatchResultsDiskIndexGrowsAndPreservesOrder(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/batches/batch_example":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","input_file_id":"file_input","output_file_id":"file_output"}`)
		case "/files/file_output/content":
			for i := 2299; i >= 0; i-- {
				_, _ = fmt.Fprintf(w, "{\"custom_id\":\"id-%d\",\"response\":{\"body\":%d}}\n", i, i)
			}
			_, _ = io.WriteString(w, "{\"custom_id\":\"id-0\",\"response\":{\"body\":9007199254740993}}\n{}\n")
		case "/files/file_input/content":
			for i := 0; i < 2300; i++ {
				_, _ = fmt.Fprintf(w, "{\"custom_id\":\"id-%d\",\"body\":{\"input\":%d}}\n", i, i)
			}
			_, _ = io.WriteString(w, "{\"custom_id\":\"id-0\",\"body\":{\"input\":\"duplicate\"}}\n{\"custom_id\":\"pending\"}\n{}\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var out bytes.Buffer
	root := batchRecordsTestCommand(t, server.URL, &out)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example", "--match-input"}))
	rows := gjson.ParseBytes(out.Bytes()).Get("data").Array()
	require.Len(t, rows, 2303)
	for i := range 2300 {
		require.Equal(t, fmt.Sprintf("id-%d", 2299-i), rows[i].Get("custom_id").Str)
		require.Equal(t, int64(2299-i), rows[i].Get("inputs.0.body.input").Int())
	}
	require.Equal(t, "ambiguous", rows[2299].Get("match").Str)
	require.Equal(t, "9007199254740993", rows[2299].Get("results.1.response.body").Raw)
	require.Equal(t, []string{"duplicate_input_custom_id", "duplicate_result_custom_id"}, []string{rows[2299].Get("issues.0").Str, rows[2299].Get("issues.1").Str})
	require.Equal(t, "missing_result_custom_id", rows[2300].Get("issues.0").Str)
	require.Equal(t, "pending", rows[2301].Get("custom_id").Str)
	require.Equal(t, "missing_result", rows[2301].Get("match").Str)
	require.Equal(t, "missing_input_custom_id", rows[2302].Get("issues.0").Str)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBatchResultsTemporaryStorageFailureHasRecovery(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, filepath.Join(directory, "missing"))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
	}))
	defer server.Close()
	var out bytes.Buffer
	root := batchRecordsTestCommand(t, server.URL, &out)
	err := root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "temporary storage space and permissions")
	require.Contains(t, err.Error(), "retry the same command")
	require.NotContains(t, err.Error(), directory)
	require.Empty(t, out.String())
}

func TestBatchResultsStreamingParserAcceptsJSONContracts(t *testing.T) {
	for name, payload := range map[string]string{
		"outer_unicode_space": "\u0085\u00a0{\"custom_id\":\"one\",\"body\":[-0,1.2300e+99,true,false,null]}\u2000\r\n",
		"escaped_key_and_id":  `{"\u0063ustom_id":"\ud83d\ude00","body":"\ud800a\udc00"}`,
		"long_keys":           `{"` + strings.Repeat("custom_id", 100) + `":"other","custom_id":"one"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/batches/batch_example" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
				} else {
					_, _ = io.WriteString(w, payload)
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			root := batchRecordsTestCommand(t, server.URL, &out)
			require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example"}))
			require.True(t, gjson.ValidBytes(out.Bytes()))
			require.Equal(t, int64(1), gjson.GetBytes(out.Bytes(), "data.#").Int())
		})
	}
}
