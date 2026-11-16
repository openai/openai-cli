package audio_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/commands/audio"
	appconfig "github.com/openai/openai-cli/internal/app"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAudioTextPresentationFormatsAndCancellation(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"auto", "Spoken words\n"}, {"text", "Spoken words\n"},
		{"raw", "Spoken words"}, {"json", "\"Spoken words\"\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			ctx, cancel := context.WithCancel(audio.TextContextForTest(t.Context(), "Spoken words"))
			defer cancel()
			var out strings.Builder
			opts := appconfig.ShowJSONOpts{Context: ctx, Operation: "(resource) audio.transcriptions > (method) create", OutputKind: appconfig.OutputResponse, Stdout: &out, Format: tc.format, ExplicitFormat: true}
			require.NoError(t, appconfig.ShowJSON(gjson.Result{}, opts))
			require.Equal(t, tc.want, out.String())
			out.Reset()
			cancel()
			require.ErrorIs(t, appconfig.ShowJSON(gjson.Result{}, opts), context.Canceled)
			require.Empty(t, out.String())
		})
	}
}

func TestAudioTextRawReportsShortWrite(t *testing.T) {
	ctx := audio.TextContextForTest(t.Context(), "Spoken words")
	err := appconfig.ShowJSON(gjson.Result{}, appconfig.ShowJSONOpts{Context: ctx, Operation: "(resource) audio.translations > (method) create", OutputKind: appconfig.OutputResponse, Stdout: shortDownloadWriter{}, Format: "raw", ExplicitFormat: true})
	require.ErrorIs(t, err, io.ErrShortWrite)
}

func TestAudioTextPreservesExtractionAndEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		name, input, format, transform, want string
		raw                                  bool
	}{
		{"empty", "", "text", "", "\n", false},
		{"raw extraction", "hello", "raw", "@tostr", "\"\\\"hello\\\"\"\n", false},
		{"raw-output extraction", "hello", "text", "@tostr", "\"hello\"\n", true},
		{"controls", "line one\n\x1b[31mline two\x1b[0m", "text", "", "line one\n\\u001b[31mline two\\u001b[0m\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := audio.TextContextForTest(t.Context(), tc.input)
			var out strings.Builder
			err := appconfig.ShowJSON(gjson.Result{}, appconfig.ShowJSONOpts{Context: ctx, Operation: "(resource) audio.transcriptions > (method) create", OutputKind: appconfig.OutputResponse, Stdout: &out, Format: tc.format, ExplicitFormat: true, RawOutput: tc.raw, Transform: tc.transform})
			require.NoError(t, err)
			require.Equal(t, tc.want, out.String())
		})
	}
}

type shortDownloadWriter struct{}

func (shortDownloadWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
