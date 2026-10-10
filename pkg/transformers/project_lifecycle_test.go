package transformers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectLifecycleReturnedData(t *testing.T) {
	for _, method := range []string{"create", "update", "archive"} {
		t.Run(method, func(t *testing.T) {
			status := "active"
			if method == "archive" {
				status = "archived"
			}
			input := `{"id":"proj_returned","object":"organization.project","name":"Returned name","status":"` + status + `","residency":"GLOBAL","created_at":9007199254740993,"archived_at":null,"external_key_id":null}`
			value := gjson.Parse(input)
			receipt, ok, err := ProjectLifecycle(t.Context(), value, lifecycleRoute(method))
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "proj_returned", receipt.ID)
			require.Equal(t, "Returned name", receipt.Details.Get("name").Str)
			require.Equal(t, "9007199254740993", receipt.Details.Get("created_at").Raw)
			require.Equal(t, "GLOBAL", receipt.Details.Get("residency").Str)
			require.Equal(t, "null", receipt.Details.Get("external_key_id").Raw)
			require.Equal(t, input, value.Raw)
		})
	}
}

func TestProjectLifecycleFallback(t *testing.T) {
	valid := `{"id":"proj_demo","object":"organization.project","status":"active"}`
	for _, input := range []string{
		`null`, `[]`, `{}`, strings.TrimSuffix(valid, "}"),
		strings.Replace(valid, `"proj_demo"`, `""`, 1),
		strings.Replace(valid, `"active"`, `null`, 1),
		strings.Replace(valid, `"active"`, `"archived"`, 1),
		strings.Replace(valid, `"organization.project"`, `"file"`, 1),
		strings.TrimSuffix(valid, "}") + `,"id":"other"}`,
		strings.TrimSuffix(valid, "}") + `,"error":{"message":"failed"}}`,
		strings.TrimSuffix(valid, "}") + `,"future":"preserve me"}`,
		strings.TrimSuffix(valid, "}") + `,"name":[]}`,
		strings.TrimSuffix(valid, "}") + `,"created_at":"invalid"}`,
	} {
		_, ok, err := ProjectLifecycle(t.Context(), gjson.Parse(input), lifecycleRoute("update"))
		require.NoError(t, err, input)
		require.False(t, ok, input)
	}
	for _, route := range []Route{lifecycleRoute("retrieve"), lifecycleRoute("list"), {Operation: lifecycleRoute("create").Operation, OutputKind: OutputStreamEvent}, {Operation: "(resource) projects > (method) create", OutputKind: OutputResponse}} {
		_, ok, err := ProjectLifecycle(t.Context(), gjson.Parse(valid), route)
		require.NoError(t, err)
		require.False(t, ok)
	}
	_, ok, err := ProjectLifecycle(t.Context(), gjson.Parse(valid), lifecycleRoute("archive"))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestProjectLifecycleCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, ok, err := ProjectLifecycle(ctx, gjson.Parse(`{}`), lifecycleRoute("create"))
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ok)
}

func lifecycleRoute(method string) Route {
	return Route{Operation: "(resource) admin.organization.projects > (method) " + method, OutputKind: OutputResponse}
}
