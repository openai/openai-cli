package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSpendMutationResponseObjectValidation(t *testing.T) {
	for _, route := range []struct{ operation, object string }{
		{"admin.organization.spend_limit > (method) update", "organization.spend_limit"},
		{"admin.organization.spend_limit > (method) delete", "organization.spend_limit.deleted"},
		{"admin.organization.projects.spend_limit > (method) update", "project.spend_limit"},
		{"admin.organization.projects.spend_limit > (method) delete", "project.spend_limit.deleted"},
		{"admin.organization.spend_alerts > (method) create", "organization.spend_alert"},
		{"admin.organization.spend_alerts > (method) update", "organization.spend_alert"},
		{"admin.organization.spend_alerts > (method) delete", "organization.spend_alert.deleted"},
		{"admin.organization.projects.spend_alerts > (method) create", "project.spend_alert"},
		{"admin.organization.projects.spend_alerts > (method) update", "project.spend_alert"},
		{"admin.organization.projects.spend_alerts > (method) delete", "project.spend_alert.deleted"},
		{"admin.organization.projects.rate_limits > (method) update_rate_limit", "project.rate_limit"},
	} {
		t.Run(route.operation, func(t *testing.T) {
			otherOperation := route.object + ".deleted"
			if strings.HasSuffix(route.object, ".deleted") {
				otherOperation = strings.TrimSuffix(route.object, ".deleted")
			}
			for _, invalid := range []struct{ name, body string }{
				{"null", `null`},
				{"array", `[]`},
				{"string", `"synthetic-private-response"`},
				{"number", `123`},
				{"boolean", `true`},
				{"missing", `{"private":"synthetic-private-response"}`},
				{"null_object", `{"object":null}`},
				{"number_object", `{"object":1}`},
				{"wrong_object", `{"object":"synthetic-private-other"}`},
				{"wrong_operation", fmt.Sprintf(`{"object":%q}`, otherOperation)},
				{"wrong_scope", fmt.Sprintf(`{"object":%q}`, strings.ReplaceAll(strings.ReplaceAll(route.object, "organization.", "other."), "project.", "other."))},
				{"duplicate_same", fmt.Sprintf(`{"object":%q,"object":%q}`, route.object, route.object)},
				{"duplicate_other", fmt.Sprintf(`{"object":%q,"object":"synthetic-private-other"}`, route.object)},
				{"duplicate_escaped", fmt.Sprintf(`{"object":%q,"\u006fbject":%q}`, route.object, route.object)},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					var out, diagnostic bytes.Buffer
					err := ShowJSON(gjson.Parse(invalid.body), ShowJSONOpts{Context: t.Context(),
						Operation: "(resource) " + route.operation, OutputKind: OutputResponse,
						Format: "json", Stdout: &out, Stderr: &diagnostic})
					var marker *spendMutationResponseError
					var presentation *spendMutationPresentationError
					require.ErrorAs(t, err, &marker)
					require.False(t, errors.As(err, &presentation))
					require.NotContains(t, err.Error(), "synthetic-private")
					require.Empty(t, out.String())
					require.Empty(t, diagnostic.String())
				})
			}
			// This boundary validates only the discriminator. Preserve unknown fields and exact bytes.
			response := fmt.Sprintf(`{"object":%q,"id":null,"future":{"exact":9007199254740993,"decimal":1.00000000000000001},"unknown":1,"unknown":2}`, route.object)
			var out bytes.Buffer
			require.NoError(t, ShowJSON(gjson.Parse(response), ShowJSONOpts{Context: t.Context(),
				Operation: "(resource) " + route.operation, OutputKind: OutputResponse,
				Format: "raw", Stdout: &out, Stderr: io.Discard}))
			require.Equal(t, response, strings.TrimSpace(out.String()))
			response = strings.Replace(response, `"object"`, `"\u006fbject"`, 1)
			out.Reset()
			require.NoError(t, ShowJSON(gjson.Parse(response), ShowJSONOpts{Context: t.Context(),
				Operation: "(resource) " + route.operation, OutputKind: OutputResponse,
				Format: "raw", Stdout: &out, Stderr: io.Discard}))
			require.Equal(t, response, strings.TrimSpace(out.String()))
		})
	}
}

func TestSpendMutationResponseValidationBeforePresentation(t *testing.T) {
	for _, mode := range []string{"text", "json", "jsonl", "pretty", "raw", "yaml", "explore", "extraction"} {
		t.Run(mode, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) admin.organization.spend_alerts > (method) create",
				OutputKind: OutputResponse, Format: mode, ExplicitFormat: true, Stdout: &out, Stderr: &diagnostic}
			if mode == "extraction" {
				opts.Format, opts.Transform = "json", "future"
			}
			err := ShowJSON(gjson.Parse(`{"future":{"object":"organization.spend_alert"}}`), opts)
			var marker *spendMutationResponseError
			require.ErrorAs(t, err, &marker)
			require.Empty(t, out.String())
			require.Empty(t, diagnostic.String(), "validation must precede format fallback warnings")
		})
	}
	for _, control := range []struct {
		name, operation string
		kind            OutputKind
	}{
		{"read", "(resource) admin.organization.spend_alerts > (method) retrieve", OutputResponse},
		{"other_mutation", "(resource) models > (method) delete", OutputResponse},
		{"page", "(resource) admin.organization.spend_alerts > (method) create", OutputPageItem},
		{"error", "", OutputUnspecified},
	} {
		t.Run(control.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, ShowJSON(gjson.Parse(`null`), ShowJSONOpts{Context: t.Context(),
				Operation: control.operation, OutputKind: control.kind, Format: "json", Stdout: &out, Stderr: io.Discard}))
			require.Equal(t, "null", strings.TrimSpace(out.String()))
		})
	}
}
