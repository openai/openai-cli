package custom

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFileReceiptPreservesUnfamiliarProcessingStatus(t *testing.T) {
	for _, status := range []string{`"failed"`, `"pending"`, `""`, `false`, `1`, `{ "error": "synthetic failure" }`, `[]`} {
		value := gjson.Parse(strings.TrimSuffix(fileReceiptFixture, "}") + `,"status":` + status + "}")
		require.False(t, validFileReceipt(value), "status %s must retain full response presentation", status)
	}
	for _, status := range []string{`"uploaded"`, `"processed"`, `"error"`, `null`} {
		value := gjson.Parse(strings.TrimSuffix(fileReceiptFixture, "}") + `,"status":` + status + "}")
		require.True(t, validFileReceipt(value), status)
	}
}

func TestFileReceiptProcessingFailureSuggestsInspection(t *testing.T) {
	value := gjson.Parse(strings.TrimSuffix(fileReceiptFixture, "}") + `,"status":"error","status_details":"synthetic processing failure"}`)
	var out bytes.Buffer
	require.NoError(t, writeFileReceipt(&out, value, "bash", fileInvocation{display: "openai"}))
	require.Contains(t, out.String(), "Uploaded upload space.txt (13 B)")
	require.Contains(t, out.String(), "Status: error")
	require.Contains(t, out.String(), "Status details: synthetic processing failure")
	require.Contains(t, out.String(), "Inspect it: openai files get file-example --format json")
	require.NotContains(t, out.String(), "Download it:")
}

func TestFileReceiptRejectsInvalidUTF8Fields(t *testing.T) {
	for _, field := range []string{"id", "filename", "purpose"} {
		raw := `{"object":"file","id":"file-example","filename":"upload.txt","purpose":"user_data"}`
		old := map[string]string{"id": "file-example", "filename": "upload.txt", "purpose": "user_data"}[field]
		value := gjson.Parse(strings.Replace(raw, old, "bad\xffvalue", 1))
		require.False(t, validFileReceipt(value), field)
		if field != "purpose" {
			var out bytes.Buffer
			require.NoError(t, writeFileReceipt(&out, value, "bash", fileInvocation{display: "openai"}))
			require.NotContains(t, out.String(), "Download it:", field)
		}
	}
}
