package transform

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOrganizationProjectDetails(t *testing.T) {
	input := `{"object":"organization.project","id":"proj_demo","created_at":1700000000,"name":"Test project","status":"active","residency":"GLOBAL","archived_at":null,"psp_decryption_key_id":null}`
	details, err := OrganizationProjectDetails(t.Context(), gjson.Parse(input))
	require.NoError(t, err)
	require.Equal(t, `{"name":"Test project","id":"proj_demo","status":"active","created_at":"2023-11-14 22:13:20 UTC","residency":"GLOBAL"}`, details.Raw)

	archived := `{"object":"organization.project","id":"proj_demo","archived_at":1700000000,"created_at":253402300800,"status":"archived","psp_decryption_key_id":"key_identifier"}`
	details, err = OrganizationProjectDetails(t.Context(), gjson.Parse(archived))
	require.NoError(t, err)
	require.Equal(t, "2023-11-14 22:13:20 UTC", details.Get("archived_at").Str)
	require.Equal(t, "253402300800", details.Get("created_at").Raw, "out-of-range timestamps stay exact")
	require.Equal(t, "key_identifier", details.Get("psp_decryption_key_id").Str)

	malformedFields := `{"object":"organization.project","id":"proj_demo","name":[],"status":"","archived_at":{},"psp_decryption_key_id":{}}`
	details, err = OrganizationProjectDetails(t.Context(), gjson.Parse(malformedFields))
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"proj_demo","name":[],"status":"","archived_at":{},"psp_decryption_key_id":{}}`, details.Raw)

	for _, extra := range []string{`,"future":null}`, `,"id":"other"}`, `,"error":"oops"}`} {
		details, err = OrganizationProjectDetails(t.Context(), gjson.Parse(strings.TrimSuffix(input, "}")+extra))
		require.NoError(t, err)
		require.False(t, details.Exists(), "ambiguous and unknown records use the full view")
	}
}

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
