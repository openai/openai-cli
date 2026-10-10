package transformers

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExternalStorageValidationStates(t *testing.T) {
	for status, note := range map[string]string{
		"pending":   "not complete",
		"validated": "does not establish continuous storage health",
		"unhealthy": "Storage needs attention",
	} {
		for _, method := range []string{"create", "retrieve", "validate", "list"} {
			t.Run(status+"/"+method, func(t *testing.T) {
				kind := OutputResponse
				if method == "list" {
					kind = OutputPageItem
				}
				input := gjson.Parse(`{"object":"organization.external_storage","id":"ext_actual","project_id":"proj_actual","status":"` + status + `","provider":{"type":"future","n":9007199254740993},"error":{"message":"synthetic failure"},"future":null}`)
				result, err := Select(Route{"(resource) admin.organization.external_storage > (method) " + method, kind})(t.Context(), input)
				require.NoError(t, err)
				require.Equal(t, status, result.Get("validation_status").Str)
				require.Contains(t, result.Get("validation_note").Str, note)
				for _, key := range []string{"object", "id", "project_id", "provider", "error", "future"} {
					require.Equal(t, input.Get(key).Raw, result.Get(key).Raw, key)
				}
			})
		}
	}
}

func TestExternalStoragePreservesUnfamiliarResponses(t *testing.T) {
	for _, fields := range []string{
		``, `,"status":null`, `,"status":""`, `,"status":42`, `,"status":"future"`,
		`,"status":"pending","validation_note":null`,
		`,"status":"pending","validation_status":"future"`,
		`,"status":"pending","status":"validated"`,
		`,"status":"pending","provider":{},"provider":null`,
	} {
		input := gjson.Parse(`{"object":"organization.external_storage"` + fields + `}`)
		result, err := projectExternalStorage(t.Context(), input)
		require.NoError(t, err)
		require.Equal(t, input.Raw, result.Raw)
	}
	input := gjson.Parse(`{"object":"organization.external_storage","status":"pending"`)
	result, err := projectExternalStorage(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, input.Raw, result.Raw)
}
