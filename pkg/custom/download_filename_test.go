package custom

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDownloadFilenamePreservesBytes(t *testing.T) {
	for _, tc := range []struct{ name, header, want string }{
		{"legacy invalid bytes", "attachment; filename=\"report-\xff\xfe.bin\"", "report-\xff\xfe.bin"},
		{"encoded invalid bytes and controls", "attachment; filename*=UTF-8''report-%FF%09%C2%9B.bin", "report-\xff__.bin"},
		{"valid Unicode", "attachment; filename*=UTF-8''r%C3%A9sum%C3%A9.bin", "résumé.bin"},
		{"maximum byte component", "attachment; filename=\"" + strings.Repeat("\xff", 251) + ".bin\"", strings.Repeat("\xff", 251) + ".bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, params, err := mime.ParseMediaType(tc.header)
			require.NoError(t, err)
			require.Equal(t, tc.want, normalizeDownloadFilename(params["filename"]))
		})
	}
}

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

func TestCreateDownloadFilePreservesInvalidUTF8(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames use Unicode rather than arbitrary byte strings")
	}
	for _, tc := range []struct{ name, filename, want string }{
		{"legacy header", "report-\xff\xfe.bin", "report-\xff\xfe.bin"},
		{"controls beside invalid bytes", "report-\xff\t\u009b.bin", "report-\xff__.bin"},
		{"maximum byte component", strings.Repeat("\xff", 251) + ".bin", strings.Repeat("\xff", 251) + ".bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			// Verify the filesystem accepts the unchanged destination before checking
			// header handling, including the 255-byte component boundary.
			if err := os.WriteFile(tc.want, nil, 0600); err != nil {
				t.Skipf("filesystem does not support this byte filename: %v", err)
			}
			require.NoError(t, os.Remove(tc.want))
			response := &http.Response{Header: http.Header{"Content-Disposition": {"attachment; filename=\"" + tc.filename + "\""}}}
			file, err := createDownloadFile(response, []byte{0xff})
			require.NoError(t, err)
			require.Equal(t, tc.want, filepath.Base(file.Name()))
			_, err = file.WriteString("synthetic bytes")
			require.NoError(t, err)
			require.NoError(t, file.Close())
			data, err := os.ReadFile(tc.want)
			require.NoError(t, err)
			require.Equal(t, "synthetic bytes", string(data))
		})
	}
}

func TestCreateDownloadFileLongNormalizedCollision(t *testing.T) {
	for _, name := range []string{
		strings.Repeat("a", 250) + "\t.bin",
		strings.Repeat("é", 125) + "\t.bin",
		"a." + strings.Repeat("a", 252) + "\t",
	} {
		t.Run(fmt.Sprintf("bytes=%d/extension=%d", len(name), len(filepath.Ext(name))), func(t *testing.T) {
			t.Chdir(t.TempDir())
			occupied := strings.ReplaceAll(name, "\t", "_")
			require.NoError(t, os.WriteFile(occupied, []byte("keep"), 0600))
			response := &http.Response{Header: http.Header{"Content-Disposition": {"attachment; filename*=UTF-8''" + url.PathEscape(name)}}}
			file, err := createDownloadFile(response, []byte("%PDF-1.7\nsynthetic"))
			require.NoError(t, err)
			require.NotEqual(t, occupied, filepath.Base(file.Name()))
			_, err = file.WriteString("new download")
			require.NoError(t, err)
			require.NoError(t, file.Close())
			assertDownloadPermissions(t, file.Name(), 0600)
			data, err := os.ReadFile(occupied)
			require.NoError(t, err)
			require.Equal(t, "keep", string(data))
		})
	}
}
