package skills

import (
	"crypto/rand"
	"os"

	"github.com/openai/openai-cli/internal/windowsfile"
)

func archiveTemp() (*os.File, error) {
	root, err := os.OpenRoot(os.TempDir())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return windowsfile.CreateTemporary(root, "openai-skill-"+rand.Text()+".zip")
}
