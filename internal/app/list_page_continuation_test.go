package app

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestListPageContinuationObservesOnlyEnvelope(t *testing.T) {
	for _, tc := range []struct {
		body string
		more bool
	}{
		{`{"data":[],"has_more":true}`, true},
		{`{"has_more":true,"data":[{"has_more":false}]}`, true},
		{`{"data":[{"has_more":true}],"has_more":false}`, false},
		{`{"data":[{"has_more":true}],"last_id":"has_more"}`, false},
		{`{"data":[{"id":"\"}has_more\":true"}]}`, false},
		{`{"has_\u006dore": true}`, true},
		{`{"\u0068\u0061\u0073\u005f\u006d\u006f\u0072\u0065":true}`, true},
		{`{"has_more":true,"has_more":false}`, false},
		{`{"` + strings.Repeat("x", 100) + `has_more":true}`, false},
	} {
		// Split at every character, including quotes, escapes and the boolean.
		page := &listPageContinuation{ReadCloser: io.NopCloser(iotest.OneByteReader(strings.NewReader(tc.body)))}
		got, err := io.ReadAll(page)
		require.NoError(t, err)
		require.Equal(t, tc.body, string(got))
		require.Equal(t, tc.more, page.more, tc.body)
	}
}

func BenchmarkListPageContinuationLargeRecord(b *testing.B) {
	body := []byte(`{"data":[{"text":"` + strings.Repeat("x", 8<<20) + `"}],"has_more":true}`)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		page := &listPageContinuation{ReadCloser: io.NopCloser(bytes.NewReader(body))}
		if _, err := io.Copy(io.Discard, page); err != nil || !page.more {
			b.Fatal("incomplete read", err)
		}
	}
}
