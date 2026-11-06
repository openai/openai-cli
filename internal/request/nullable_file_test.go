package request

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
)

func TestEmbedNullableStrings(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic content"), 0600))
	for _, style := range []FileEmbedStyle{EmbedText, EmbedIOReader} {
		got, err := EmbedFiles(map[string]any{
			"reference": requestflag.Ptr("@" + path),
			"escaped":   requestflag.Ptr(`\@literal`),
			"null":      (*string)(nil),
			"number":    requestflag.Ptr[int64](42),
		}, style, nil)
		require.NoError(t, err)
		fields := got.(map[string]any)
		if style == EmbedIOReader {
			reader := fields["reference"].(io.ReadCloser)
			data, err := io.ReadAll(reader)
			require.NoError(t, reader.Close())
			require.NoError(t, err)
			fields["reference"] = string(data)
		}
		encoded, err := json.Marshal(fields)
		require.NoError(t, err)
		require.JSONEq(t, `{"reference":"synthetic content","escaped":"@literal","null":null,"number":42}`, string(encoded))
	}
}
