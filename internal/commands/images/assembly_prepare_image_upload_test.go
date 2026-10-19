package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImageUploadPreparationClosesWhenActionAborts(t *testing.T) {
	images.CheckTestImageUploadPreparationClosesWhenActionAborts(assemblyForTest(), t)
}
