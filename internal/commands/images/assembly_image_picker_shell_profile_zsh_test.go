package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImagePickerZshStartupChild(t *testing.T) {
	images.CheckTestImagePickerZshStartupChild(assemblyForTest(), t)
}
