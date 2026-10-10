package transformers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDataRetentionConfiguredPolicy(t *testing.T) {
	for _, scope := range []string{"organization", "project"} {
		resource := "admin.organization.data_retention"
		settings := []string{"zero_data_retention", "modified_abuse_monitoring", "enhanced_zero_data_retention", "enhanced_modified_abuse_monitoring"}
		if scope == "project" {
			resource = "admin.organization.projects.data_retention"
			settings = append(settings, "organization_default", "none")
		}
		for _, method := range []string{"retrieve", "update"} {
			for _, setting := range settings {
				t.Run(scope+"/"+method+"/"+setting, func(t *testing.T) {
					input := gjson.Parse(`{"object":"` + scope + `.data_retention","type":"` + setting + `"}`)
					result, err := Select(Route{"(resource) " + resource + " > (method) " + method, OutputResponse})(t.Context(), input)
					require.NoError(t, err)
					require.Equal(t, input.Get("object"), result.Get("object"))
					require.Contains(t, result.Get("configured_retention").Str, setting)
					require.Equal(t, "not resolved by this response", result.Get("effective_retention").Str)
					if setting == "organization_default" {
						require.Contains(t, result.Get("configured_retention").Str, "inherit organization default")
					}
				})
			}
		}
	}
}

func TestDataRetentionPreservesUnfamiliarResponses(t *testing.T) {
	transform := projectDataRetention("project.data_retention")
	for _, input := range []string{
		`{"object":"project.data_retention"}`,
		`{"object":"project.data_retention","type":null}`,
		`{"object":"project.data_retention","type":""}`,
		`{"object":"project.data_retention","type":false}`,
		`{"object":"project.data_retention","type":"future_policy"}`,
		`{"object":"project.data_retention","type":"none","future":null}`,
		`{"object":"project.data_retention","type":"none","effective_retention":"future"}`,
		`{"object":"project.data_retention","type":"none","type":"organization_default"}`,
		`{"object":"project.data_retention","type":"none","object":"wrong"}`,
		`{"object":"wrong","type":"none"}`,
		`{"object":"project.data_retention","type":"none"`,
		`null`,
	} {
		t.Run(input, func(t *testing.T) {
			value := gjson.Parse(input)
			result, err := transform(t.Context(), value)
			require.NoError(t, err)
			require.Equal(t, value.Raw, result.Raw)
		})
	}
}

func TestDataControlsBoundariesAndCancellation(t *testing.T) {
	for _, resource := range []string{"admin.organization.data_retention", "admin.organization.projects.data_retention", "admin.organization.external_storage"} {
		for _, boundary := range []OutputKind{OutputUnspecified, OutputStreamEvent} {
			for _, method := range []string{"retrieve", "update", "create", "validate", "list", "delete"} {
				require.Nil(t, selectDataControlsTransformer(Route{"(resource) " + resource + " > (method) " + method, boundary}))
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, transform := range []Transformer{projectDataRetention("project.data_retention"), projectExternalStorage} {
		_, err := transform(ctx, gjson.Parse(`{}`))
		require.ErrorIs(t, err, context.Canceled)
	}
}
