package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestInteractiveImageFailureKeepsExitCodeAndViewerDiagnostic(t *testing.T) {
	images.CheckTestInteractiveImageFailureKeepsExitCodeAndViewerDiagnostic(assemblyForTest(), t)
}
