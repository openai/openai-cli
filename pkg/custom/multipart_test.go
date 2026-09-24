package custom

import (
	"path/filepath"
	"testing"
)

func setUnavailableTempDir(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
}
