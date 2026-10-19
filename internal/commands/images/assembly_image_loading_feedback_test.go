package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImageLoadingLeavesNoninteractiveActionUnchanged(t *testing.T) {
	images.CheckTestImageLoadingLeavesNoninteractiveActionUnchanged(assemblyForTest(), t)
}
