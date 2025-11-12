// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/openai/openai-cli/internal/requestflag"
)

func TestContainersCreate(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"containers", "create",
			"--name", "name",
			"--expires-after", "{anchor: last_active_at, minutes: 0}",
			"--file-id", "string",
			"--memory-limit", "1g",
			"--network-policy", "{type: disabled}",
			"--skill", "{skill_id: x, type: skill_reference, version: version}",
		)
	})

	t.Run("inner flags", func(t *testing.T) {
		// Check that inner flags have been set up correctly
		requestflag.CheckInnerFlags(containersCreate)

		// Alternative argument passing style using inner flags
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"containers", "create",
			"--name", "name",
			"--expires-after.anchor", "last_active_at",
			"--expires-after.minutes", "0",
			"--file-id", "string",
			"--memory-limit", "1g",
			"--network-policy", "{type: disabled}",
			"--skill", "{skill_id: x, type: skill_reference, version: version}",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"name: name\n" +
			"expires_after:\n" +
			"  anchor: last_active_at\n" +
			"  minutes: 0\n" +
			"file_ids:\n" +
			"  - string\n" +
			"memory_limit: 1g\n" +
			"network_policy:\n" +
			"  type: disabled\n" +
			"skills:\n" +
			"  - skill_id: x\n" +
			"    type: skill_reference\n" +
			"    version: version\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"--api-key", "string",
			"--admin-api-key", "string",
			"containers", "create",
		)
	})
}

func TestContainersRetrieve(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"containers", "retrieve",
			"--container-id", "container_id",
		)
	})
}

func TestContainersList(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"containers", "list",
			"--max-items", "10",
			"--after", "after",
			"--limit", "0",
			"--name", "name",
			"--order", "asc",
		)
	})
}

func TestContainersDelete(t *testing.T) {
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"--api-key", "string",
			"--admin-api-key", "string",
			"containers", "delete",
			"--container-id", "container_id",
		)
	})
}
