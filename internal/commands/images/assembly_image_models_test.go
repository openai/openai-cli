package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImageModelsErrorClassification(t *testing.T) {
	images.CheckTestImageModelsErrorClassification(assemblyForTest(), t)
}
func TestImageModelsUsesConfiguredMTLSClient(t *testing.T) {
	images.CheckTestImageModelsUsesConfiguredMTLSClient(assemblyForTest(), t)
}
func TestImageModelsDataFormatsAndExtraction(t *testing.T) {
	images.CheckTestImageModelsDataFormatsAndExtraction(assemblyForTest(), t)
}
