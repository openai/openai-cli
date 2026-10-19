package custom

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateDownloadFileNormalizesDecodedControls(t *testing.T) {
	for _, control := range []rune{0, '\t', '\n', '\r', 0x1b, 0x7f, 0x80, 0x85, 0x9b, 0x9d, 0x9f} {
		t.Run(fmt.Sprintf("U+%04X", control), func(t *testing.T) {
			t.Chdir(t.TempDir())
			name := "résumé " + string(control) + ".bin"
			response := &http.Response{Header: http.Header{"Content-Disposition": {"attachment; filename*=UTF-8''" + url.PathEscape(name)}}}
			file, err := createDownloadFile(response, []byte{0xff})
			require.NoError(t, err)
			require.Equal(t, "résumé _.bin", filepath.Base(file.Name()))
			_, err = file.WriteString("original")
			require.NoError(t, err)
			require.NoError(t, file.Close())
			assertDownloadPermissions(t, file.Name(), 0600)
			// A normalized-name collision still creates a new private file.
			other, err := createDownloadFile(response, []byte{0xff})
			require.NoError(t, err)
			require.NotEqual(t, file.Name(), other.Name())
			require.True(t, strings.HasPrefix(filepath.Base(other.Name()), "résumé _-"))
			require.Equal(t, ".bin", filepath.Ext(other.Name()))
			require.NoError(t, other.Close())
			assertDownloadPermissions(t, other.Name(), 0600)
			data, err := os.ReadFile(file.Name())
			require.NoError(t, err)
			require.Equal(t, "original", string(data))
		})
	}
}
