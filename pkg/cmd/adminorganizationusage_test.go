// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestAdminOrganizationUsageAudioSpeeches(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "audio-speeches",
			"--start-time", "0",
			"--api-key-id", "string",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--model", "string",
			"--page", "page",
			"--project-id", "string",
			"--user-id", "string",
		)
	})
}

func TestAdminOrganizationUsageAudioTranscriptions(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "audio-transcriptions",
			"--start-time", "0",
			"--api-key-id", "string",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--model", "string",
			"--page", "page",
			"--project-id", "string",
			"--user-id", "string",
		)
	})
}

func TestAdminOrganizationUsageCodeInterpreterSessions(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "code-interpreter-sessions",
			"--start-time", "0",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--page", "page",
			"--project-id", "string",
		)
	})
}

func TestAdminOrganizationUsageCompletions(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "completions",
			"--start-time", "0",
			"--api-key-id", "string",
			"--batch=true",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--model", "string",
			"--page", "page",
			"--project-id", "string",
			"--user-id", "string",
		)
	})
}

func TestAdminOrganizationUsageCosts(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "costs",
			"--start-time", "0",
			"--api-key-id", "string",
			"--bucket-width", "1d",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--page", "page",
			"--project-id", "string",
		)
	})
}

func TestAdminOrganizationUsageEmbeddings(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "embeddings",
			"--start-time", "0",
			"--api-key-id", "string",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--model", "string",
			"--page", "page",
			"--project-id", "string",
			"--user-id", "string",
		)
	})
}

func TestAdminOrganizationUsageImages(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "images",
			"--start-time", "0",
			"--api-key-id", "string",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--model", "string",
			"--page", "page",
			"--project-id", "string",
			"--size", "256x256",
			"--source", "image.generation",
			"--user-id", "string",
		)
	})
}

func TestAdminOrganizationUsageModerations(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "moderations",
			"--start-time", "0",
			"--api-key-id", "string",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--model", "string",
			"--page", "page",
			"--project-id", "string",
			"--user-id", "string",
		)
	})
}

func TestAdminOrganizationUsageVectorStores(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"admin:organization:usage", "vector-stores",
			"--start-time", "0",
			"--bucket-width", "1m",
			"--end-time", "0",
			"--group-by", "project_id",
			"--limit", "0",
			"--page", "page",
			"--project-id", "string",
		)
	})
}
