package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
)

func TestBatchRecordStorageErrorGuidance(t *testing.T) {
	storage := &readable.JSONStreamStorageError{}
	failure := fmt.Errorf("synthetic-private-directory: %w", storage)
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			root := readableErrorTestCommand(t, "--format-error", format)
			require.NoError(t, ShowCommandError(root, failure, &out))
			message := out.String()
			if format == "json" {
				var payload map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &payload))
				message, _ = payload["message"].(string)
			}
			require.Contains(t, message, storage.Error())
			require.NotContains(t, message, "synthetic-private-directory")
		})
	}
}
