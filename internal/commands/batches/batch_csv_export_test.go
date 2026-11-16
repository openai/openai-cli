package batches

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Export is read-only on the API. The batch's original input proves every
// source row belongs to this batch, even when some requests have no result.
func exportBatchCSV(ctx context.Context, client openai.Client, options []option.RequestOption, batch gjson.Result, sourcePath, destPath string) (err error) {
	if err := validateBatchCSVResults(batch); err != nil {
		return err
	}
	if _, statErr := os.Lstat(destPath); !errors.Is(statErr, os.ErrNotExist) {
		return batchError("The CSV destination exists or cannot be checked. Choose a new filename in an accessible folder.")
	}
	store, err := newBatchScratchStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.close()) }()
	file, _, err := store.temporaryFile()
	if err != nil {
		return err
	}
	if err := writeBatchCSVResults(ctx, client, options, batch, sourcePath, file, nil); err != nil {
		return err
	}
	return saveBatchCSVResults(ctx, file, destPath)
}

func TestBatchCSVExportReordersAndRetainsEveryRow(t *testing.T) {
	dir := t.TempDir()
	source, dest := filepath.Join(dir, "tickets.csv"), filepath.Join(dir, "joined.csv")
	data := "id,review,result,Error,=unsafe\r\n001,\"Great, thanks\",a,b,c\r\n002,\"Line one\nLine two\",d,e,f\r\n003,=unsafe,g,h,i\r\n004,No answer,j,k,l\r\n"
	require.NoError(t, os.WriteFile(source, []byte(data), 0600))
	rows, err := csv.NewReader(strings.NewReader(data)).ReadAll()
	require.NoError(t, err)
	ids := make([]string, len(rows)-1)
	var inputs strings.Builder
	for i, row := range rows[1:] {
		ids[i] = batchCSVRowID(rows[0], row, i+1)
		fmt.Fprintf(&inputs, "{\"custom_id\":%q}\n", ids[i])
	}

	output := fmt.Sprintf("{\"custom_id\":%q,\"response\":{\"status_code\":200,\"body\":{\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"%s\"}]}]}}}\n", ids[2], "=unsafe")
	output += strings.ReplaceAll(strings.ReplaceAll(output, ids[2], ids[0]), "=unsafe", "billing")
	errorRecord := fmt.Sprintf("{\"custom_id\":%q,\"error\":{\"code\":\"denied\",\"message\":\"Synthetic failure\"}}\n", ids[1])
	payloads := map[string]string{"input": inputs.String(), "output": output, "error": errorRecord}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/files/"), "/content")
		data, ok := payloads[id]
		require.True(t, ok, "unexpected API route %s", r.URL.Path)
		w.Header().Set("Content-Type", "application/jsonl")
		fmt.Fprint(w, data)
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithMaxRetries(0), option.WithAPIKey("synthetic-export-key"))
	batch := gjson.Parse(`{"status":"completed","input_file_id":"input","output_file_id":"output","error_file_id":"error"}`)
	require.NoError(t, exportBatchCSV(t.Context(), client, nil, batch, source, dest))
	written, err := os.ReadFile(dest)
	require.NoError(t, err)
	actual, err := csv.NewReader(strings.NewReader(string(written))).ReadAll()
	require.NoError(t, err)
	require.Equal(t, [][]string{
		{"id", "review", "result", "Error", "'=unsafe", "result_2", "error_2"},
		{"001", "Great, thanks", "a", "b", "c", "billing", ""},
		{"002", "Line one\nLine two", "d", "e", "f", "", "Synthetic failure"},
		{"003", "'=unsafe", "g", "h", "i", "'=unsafe", ""},
		{"004", "No answer", "j", "k", "l", "", "No result returned for this request."},
	}, actual)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dest)
		require.NoError(t, err)
		require.EqualValues(t, 0600, info.Mode().Perm())
	}
	require.Error(t, exportBatchCSV(t.Context(), client, nil, batch, source, dest))
	unchanged, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, written, unchanged)
}

func TestBatchCSVExportRejectsAmbiguityAndSourceMismatch(t *testing.T) {
	header, row := []string{"id", "text"}, []string{"001", "private-text"}
	id := batchCSVRowID(header, row, 1)
	record := fmt.Sprintf("{\"custom_id\":%q}\n", id)
	other := "{\"custom_id\":\"other\"}\n"
	const data = "id,text\n001,private-text\n"
	for _, tc := range []struct {
		name, source, input, output, expected string
	}{
		{"changed cell", strings.ReplaceAll(data, "private-text", "different"), record, "", "does not match"},
		{"added row", data + "002,added\n", record, "", "does not match"},
		{"missing trailing row without result", data, record + other, "", "does not match"},
		{"duplicate original ID", data, record + record, "", "batch input file repeats a request ID"},
		{"empty remote input", data, "", "", "batch input file contains no requests"},
		{"duplicate result", data, record, record + record, "multiple result records"},
		{"unmatched result", data, record, other, "request ID missing from the batch input file"},
		{"malformed remote result", data, record, "{broken", "invalid JSONL"},
		{"malformed late source row", data + "002,private-text,extra\n", record, "", "data row 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, dest := filepath.Join(dir, "source.csv"), filepath.Join(dir, "output.csv")
			require.NoError(t, os.WriteFile(src, []byte(tc.source), 0600))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/jsonl")
				switch r.URL.Path {
				case "/files/input/content":
					fmt.Fprint(w, tc.input)
				case "/files/output/content":
					fmt.Fprint(w, tc.output)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			}))
			t.Cleanup(server.Close)
			client := openai.NewClient(option.WithBaseURL(server.URL), option.WithMaxRetries(0), option.WithAPIKey("synthetic-export-key"))
			batch := gjson.Parse(`{"status":"completed","input_file_id":"input","output_file_id":"output"}`)
			err := exportBatchCSV(t.Context(), client, nil, batch, src, dest)
			require.ErrorContains(t, err, tc.expected)
			require.NotContains(t, err.Error(), "private-text")
			if tc.name == "duplicate original ID" || tc.name == "unmatched result" || tc.name == "empty remote input" {
				var sourceErr *batchCSVSourceError
				require.NotErrorAs(t, err, &sourceErr, "choosing a different local CSV cannot repair remote records")
			}
			_, err = os.Stat(dest)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestBatchCSVExportReportsIncompleteOrRefusedResponse(t *testing.T) {
	for _, tc := range []struct{ object, answer, problem string }{
		{`{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"Synthetic refusal"}]}]}`, "", "Refused: Synthetic refusal"},
		{`{"object":"response","status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`, "partial", "Response status: incomplete."},
		{`{"object":"response","status":"completed","output":[]}`, "", "No text answer"},
		{`{}`, "", "Unrecognized response"},
	} {
		value := gjson.Parse(`{"response":{"status_code":200,"body":` + tc.object + "}}")
		text, problem := batchCSVResult(value, false)
		require.Equal(t, tc.answer, text)
		require.Contains(t, problem, tc.problem)
	}
}

func TestBatchCSVExportCancellationAndCompetingDestination(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(fmt.Sprintf("cancel_%t", cancel), func(t *testing.T) {
			dir := t.TempDir()
			src, dest := filepath.Join(dir, "source.csv"), filepath.Join(dir, "export.csv")
			require.NoError(t, os.WriteFile(src, []byte("input\nSynthetic input\n"), 0600))
			header, row := []string{"input"}, []string{"Synthetic input"}
			record := fmt.Sprintf("{\"custom_id\":%q}\n", batchCSVRowID(header, row, 1))
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/files/input/content" {
					fmt.Fprint(w, record)
					return
				}
				if r.URL.Path != "/files/output/content" {
					t.Errorf("unexpected route %s", r.URL.Path)
					return
				}
				if cancel {
					stop()
					<-r.Context().Done()
				} else {
					// The destination appears after the early preflight check.
					require.NoError(t, os.WriteFile(dest, []byte("another export"), 0600))
				}
			}))
			t.Cleanup(server.Close)
			client := openai.NewClient(option.WithBaseURL(server.URL), option.WithMaxRetries(0), option.WithAPIKey("synthetic-export-key"))
			batch := gjson.Parse(`{"status":"expired","input_file_id":"input","output_file_id":"output"}`)
			err := exportBatchCSV(ctx, client, nil, batch, src, dest)
			require.Error(t, err)
			if cancel {
				require.ErrorIs(t, err, context.Canceled)
				_, err := os.Stat(dest)
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				contents, err := os.ReadFile(dest)
				require.NoError(t, err)
				require.Equal(t, "another export", string(contents))
			}
		})
	}
}
