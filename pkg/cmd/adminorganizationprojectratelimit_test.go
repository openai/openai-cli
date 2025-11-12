// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationProjectsRateLimitsListRateLimits(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:rate-limits", "list-rate-limits",
			"--max-items", "10",
			"--project-id", "project_id",
			"--after", "after",
			"--before", "before",
			"--limit", "0",
		)
	})
}

func TestAdminOrganizationProjectsRateLimitsUpdateRateLimit(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:rate-limits", "update-rate-limit",
			"--project-id", "project_id",
			"--rate-limit-id", "rate_limit_id",
			"--batch-1-day-max-input-tokens", "0",
			"--max-audio-megabytes-per-1-minute", "0",
			"--max-images-per-1-minute", "0",
			"--max-requests-per-1-day", "0",
			"--max-requests-per-1-minute", "0",
			"--max-tokens-per-1-minute", "0",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"batch_1_day_max_input_tokens: 0\n" +
			"max_audio_megabytes_per_1_minute: 0\n" +
			"max_images_per_1_minute: 0\n" +
			"max_requests_per_1_day: 0\n" +
			"max_requests_per_1_minute: 0\n" +
			"max_tokens_per_1_minute: 0\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:projects:rate-limits", "update-rate-limit",
			"--project-id", "project_id",
			"--rate-limit-id", "rate_limit_id",
		)
	})
}
