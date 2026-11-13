package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/commands/skills"
	"github.com/stretchr/testify/require"
)

// A version option can read a scalar from another file after folder selection.
func TestSkillFolderReplacedBeforePackaging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo")
	require.NoError(t, os.Mkdir(path, 0700))
	folder, err := selectSkillFolder(path)
	require.NoError(t, err)
	require.NoError(t, os.Rename(path, path+"-old"))
	require.NoError(t, os.Mkdir(path, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("replacement"), 0600))
	upload, err := skills.ArchiveFolder(t.Context(), folder.path, folder.info)
	if upload.Reader != nil {
		defer upload.Close()
	}
	require.ErrorContains(t, err, "changed during packaging")
}
