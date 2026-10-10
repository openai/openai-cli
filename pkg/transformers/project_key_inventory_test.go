package transformers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestKeyInventoryPreservesMetadata(t *testing.T) {
	route := Route{"(resource) admin.organization.projects.api_keys > (method) list", OutputPageItem}
	input := gjson.Parse(`{"future":null,"name":"","id":"key_complete","expires_at":null,"last_used_at":0,"owner_project_access":"inactive","owner":{"type":"future","details":{}},"large":9007199254740993,"id":"duplicate","control\u001b":"kept"}`)
	got, redacted, err := ProjectKeyInventory(t.Context(), input, route)
	require.NoError(t, err)
	require.False(t, redacted)
	require.True(t, gjson.Valid(got.Raw))
	require.True(t, strings.HasPrefix(got.Raw, `{"id":"key_complete","id":"duplicate","name":""`))
	require.Contains(t, got.Raw, `"expires_at":null`)
	require.Contains(t, got.Raw, `"last_used_at":0`)
	require.Contains(t, got.Raw, `"future":null`)
	require.Contains(t, got.Raw, `"large":9007199254740993`)
	require.Contains(t, got.Raw, `"control\u001b":"kept"`)
	require.Equal(t, input.Get("owner").Raw, got.Get("owner").Raw)
	require.False(t, got.Get("created_at").Exists())
}

func TestKeyInventorySecretBoundaries(t *testing.T) {
	input := gjson.Parse(`{"value":"synthetic-one-time-secret","api_key":{"id":"key_nested","value":"synthetic-nested-secret","future":null},"metadata":{"value":"ordinary metadata"}}`)
	for _, resource := range []string{"admin.organization.admin_api_keys", "admin.organization.projects.api_keys", "admin.organization.projects.service_accounts"} {
		for _, test := range []struct {
			method string
			kind   OutputKind
			mask   bool
		}{{"list", OutputPageItem, true}, {"retrieve", OutputResponse, true}, {"create", OutputResponse, false}, {"update", OutputResponse, false}, {"delete", OutputResponse, false}, {"list", OutputResponse, false}, {"retrieve", OutputStreamEvent, false}} {
			t.Run(resource+"/"+test.method+"/"+string(test.kind), func(t *testing.T) {
				route := Route{"(resource) " + resource + " > (method) " + test.method, test.kind}
				got, redacted, err := ProjectKeyInventory(t.Context(), input, route)
				require.NoError(t, err)
				require.Equal(t, test.mask, redacted)
				if !test.mask {
					require.Equal(t, input.Raw, got.Raw)
					return
				}
				require.NotContains(t, got.Raw, "synthetic-one-time-secret")
				require.NotContains(t, got.Raw, "synthetic-nested-secret")
				require.Equal(t, "key_nested", got.Get("api_key.id").Str)
				require.Contains(t, got.Raw, `"future":null`)
				require.Equal(t, "ordinary metadata", got.Get("metadata.value").Str)
			})
		}
	}
}

func TestKeyInventoryCancellationAndLargeField(t *testing.T) {
	route := Route{"(resource) admin.organization.projects.service_accounts > (method) retrieve", OutputResponse}
	input := gjson.Parse(`{"id":"svc_demo","future":"` + strings.Repeat("x", 8*1024*1024) + `"}`)
	got, _, err := ProjectKeyInventory(t.Context(), input, route)
	require.NoError(t, err)
	require.Equal(t, input.Raw, got.Raw)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = ProjectKeyInventory(ctx, input, route)
	require.ErrorIs(t, err, context.Canceled)
}

func TestKeyInventoryMalformedResponseDoesNotExposeSecret(t *testing.T) {
	input := gjson.Parse(`{"id":"key_demo","value":"synthetic-secret"`)
	route := Route{"(resource) admin.organization.admin_api_keys > (method) retrieve", OutputResponse}
	got, _, err := ProjectKeyInventory(t.Context(), input, route)
	require.EqualError(t, err, "invalid JSON inventory response")
	require.Empty(t, got.Raw)
	got, _, err = ProjectKeyInventory(t.Context(), input, Route{"(resource) admin.organization.admin_api_keys > (method) create", OutputResponse})
	require.NoError(t, err)
	require.Equal(t, input.Raw, got.Raw)
	for _, raw := range []string{`[{"value":"synthetic-secret"}]`, `"synthetic-secret"`, `null`, `0`} {
		got, _, err := ProjectKeyInventory(t.Context(), gjson.Parse(raw), route)
		require.EqualError(t, err, "unexpected JSON inventory response: expected object")
		require.Empty(t, got.Raw)
	}
}
