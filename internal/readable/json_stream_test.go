package readable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestJSONStreamMatchesReadableOutput(t *testing.T) {
	for name, document := range map[string]string{
		"nested":         `{"batch_id":"batch_test","data":[{"custom_id":"one","inputs":[{"body":{"input":"first\nsecond\n"}}],"results":[]},{"empty":{},"numbers":[9007199254740993,-1.2300e+99,true,false,null,""],"odd.key":"value"}],"empty":[]}`,
		"strings":        `{"text":"\"\\\/\b\f\n\r\t\u001b[31m\u009b\u202e\ud83d\ude00\u2028\u2029","Line\nKey":"你好"}`,
		"array":          `[1,{},[],"a",{"x":1},null,[false,true],"z"]`,
		"empty_object":   "{}",
		"empty_list":     "[]",
		"scalar":         "9007199254740993",
		"long_string":    `{"body":"` + strings.Repeat("x", 4*1024*1024) + `\nend"}`,
		"unicode_chunks": `{"body":"` + strings.Repeat("é你好😀\u202e", 1000) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var expected, actual bytes.Buffer
			require.NoError(t, Write(&expected, gjson.Parse(document)))
			require.NoError(t, WriteJSONStream(t.Context(), &actual, strings.NewReader(document), nil))
			require.Equal(t, expected.String(), actual.String())
		})
	}
}

func TestJSONStreamUnicodeMatchesStandardDecoder(t *testing.T) {
	for _, document := range []string{
		`{"text":"\ud83d\ude00\u2028\u2029"}`,
		`{"text":"\u0061\u0000\u20ac\uD834\uDD1E"}`,
		`{"\ud800key\udc00":"\u0000\u001b\u0085\u202e"}`,
		"{\"text\":\"\xff\xc0\xaf\xed\xa0\x80\"}",
		`{"values":[-0,1.2300e+99,9007199254740993,1E-1000]}`,
	} {
		decoder := json.NewDecoder(strings.NewReader(document))
		decoder.UseNumber()
		var value any
		require.NoError(t, decoder.Decode(&value))
		normalized, err := json.Marshal(value)
		require.NoError(t, err)
		var expected, actual bytes.Buffer
		require.NoError(t, Write(&expected, gjson.ParseBytes(normalized)))
		require.NoError(t, WriteJSONStream(t.Context(), &actual, strings.NewReader(document), nil))
		require.Equal(t, expected.String(), actual.String())
	}
}

func TestJSONStreamPreservesPublishedSurrogateDecoding(t *testing.T) {
	for _, value := range []string{`"\ud800\u0061"`, `"\udc00\u0061"`, `"\ud800\ud800\udc00"`, `"\ud800x\udc00\u2028\u2029"`} {
		document := `{"text":` + value + `}`
		var expected, actual bytes.Buffer
		require.NoError(t, Write(&expected, gjson.Parse(document)))
		require.NoError(t, WriteJSONStream(t.Context(), &actual, strings.NewReader(document), nil))
		require.Equal(t, expected.String(), actual.String())
		var decoded bytes.Buffer
		require.NoError(t, DecodeJSONStringBytes(t.Context(), &decoded, strings.NewReader(value)))
		require.Equal(t, gjson.Parse(value).Str, decoded.String())
	}
}

func TestJSONStreamPreservesInvalidUTF8Keys(t *testing.T) {
	for _, key := range []string{"\xff", strings.Repeat("x", 8192) + "\xff"} {
		directory := t.TempDir()
		factory := func() (*os.File, func() error, error) {
			file, err := os.CreateTemp(directory, "key-")
			if err != nil {
				return nil, nil, err
			}
			return file, func() error { return errors.Join(file.Close(), os.Remove(file.Name())) }, nil
		}
		document := "{\"" + key + "\":\"invalid-key\",\"\ufffd\":\"replacement-key\"}"
		var expected, actual bytes.Buffer
		require.NoError(t, Write(&expected, gjson.Parse(document)))
		require.NoError(t, WriteJSONStream(t.Context(), &actual, strings.NewReader(document), factory))
		require.Equal(t, expected.String(), actual.String())
		require.Contains(t, actual.String(), `\xff`)
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Empty(t, entries)
	}
}

func TestJSONStreamSpillsLargeKeysAndReleasesBeforeValue(t *testing.T) {
	for _, key := range []string{strings.Repeat("large_key_", 10000) + "ids", strings.Repeat("x", 100000) + ".quoted\n\u202e"} {
		directory := t.TempDir()
		active, maximum := 0, 0
		factory := func() (*os.File, func() error, error) {
			file, err := os.CreateTemp(directory, "key-")
			if err != nil {
				return nil, nil, err
			}
			active++
			maximum = max(maximum, active)
			return file, func() error { active--; return errors.Join(file.Close(), os.Remove(file.Name())) }, nil
		}
		encoded, err := json.Marshal(key)
		require.NoError(t, err)
		document := "{" + string(encoded) + ":{" + string(encoded) + ":[true,{}]}}"
		var expected, actual bytes.Buffer
		require.NoError(t, Write(&expected, gjson.Parse(document)))
		require.NoError(t, WriteJSONStream(t.Context(), &actual, strings.NewReader(document), factory))
		require.Equal(t, expected.String(), actual.String())
		require.Equal(t, 1, maximum, "parent label storage must close before processing child labels")
		require.Zero(t, active)
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Empty(t, entries)
	}
}

func TestJSONStreamKeyStorageFailureRetainsCause(t *testing.T) {
	want := errors.New("synthetic private storage failure")
	factory := func() (*os.File, func() error, error) { return nil, nil, want }
	err := WriteJSONStream(t.Context(), io.Discard, strings.NewReader(`{"`+strings.Repeat("x", 8192)+`":true}`), factory)
	require.ErrorIs(t, err, want)
}

func TestDecodeJSONStringBytesPreservesInvalidUTF8Identity(t *testing.T) {
	for _, test := range []struct{ input, expected string }{
		{`"a\u0061\u2028\ud800\udc00"`, "aa\u2028\U00010000"},
		{`"\ud800a\udc00\u2028"`, "\ufffda\ufffd"},
		{"\"x\xff\xed\xa0\x80\"", "x\xff\xed\xa0\x80"},
		{`"\u0000\"\\\n"`, "\x00\"\\\n"},
	} {
		var out bytes.Buffer
		require.NoError(t, DecodeJSONStringBytes(t.Context(), &out, strings.NewReader(test.input)))
		require.Equal(t, test.expected, out.String())
	}
}

func TestJSONStreamKeyWriteFailureHasRecovery(t *testing.T) {
	directory := t.TempDir()
	cleaned := false
	factory := func() (*os.File, func() error, error) {
		file, err := os.CreateTemp(directory, "key-")
		if err != nil {
			return nil, nil, err
		}
		if err := file.Close(); err != nil {
			return nil, nil, err
		}
		return file, func() error { cleaned = true; return os.Remove(file.Name()) }, nil
	}
	err := WriteJSONStream(t.Context(), io.Discard, strings.NewReader(`{"`+strings.Repeat("x", 8192)+`":true}`), factory)
	var storage *JSONStreamStorageError
	require.ErrorAs(t, err, &storage)
	require.ErrorIs(t, err, os.ErrClosed)
	require.Contains(t, storage.Error(), "Check temporary storage space and permissions")
	require.NotContains(t, storage.Error(), directory)
	require.True(t, cleaned)
}

type canceledJSONReader struct {
	source    io.Reader
	cancel    context.CancelFunc
	remaining int
}

func (r *canceledJSONReader) Read(data []byte) (int, error) {
	if r.remaining <= 0 {
		r.cancel()
	}
	n, err := r.source.Read(data)
	r.remaining -= n
	return n, err
}

func TestJSONStreamCancellationInsideLargeScalar(t *testing.T) {
	for _, document := range []string{`{"body":"` + strings.Repeat("x", 2*1024*1024) + `"}`, `{"number":1` + strings.Repeat("0", 2*1024*1024) + `}`} {
		ctx, cancel := context.WithCancel(t.Context())
		reader := &canceledJSONReader{source: strings.NewReader(document), cancel: cancel, remaining: 8192}
		err := WriteJSONStream(ctx, io.Discard, reader, nil)
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.Greater(t, reader.remaining, -32*1024)
	}
}

type failedJSONWriter struct{ err error }

func (w failedJSONWriter) Write([]byte) (int, error) { return 0, w.err }

func TestJSONStreamRetainsWriteFailure(t *testing.T) {
	want := errors.New("synthetic output failure")
	err := WriteJSONStream(t.Context(), failedJSONWriter{want}, strings.NewReader(`{"body":"`+strings.Repeat("x", 100000)+`"}`), nil)
	require.ErrorIs(t, err, want)
}
