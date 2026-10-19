package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestDeterministicStreamCleanupKeepsSavedImageOwner(t *testing.T) {
	images.CheckTestDeterministicStreamCleanupKeepsSavedImageOwner(assemblyForTest(), t)
}
