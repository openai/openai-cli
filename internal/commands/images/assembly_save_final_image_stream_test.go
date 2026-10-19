package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestSavedImageOutputFailureRetainsFilesAndPartialError(t *testing.T) {
	images.CheckTestSavedImageOutputFailureRetainsFilesAndPartialError(assemblyForTest(), t)
}
