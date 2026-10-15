package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainDispatchFiniteJSONListMutationRoutes(t *testing.T) {
	const first = `{"id":"item_a","object":"synthetic","unknown":{"large":9007199254740993,"text":"資料-é"}}`
	const second = `{"id":"item_b","object":"synthetic","unknown":{"preserved":true}}`
	const invalid = "{\"id\":\"synthetic-private-permission\",\"unknown\":\"synthetic-private-value_\xff\"}"
	const failureMessage = "The API returned an invalid JSON list item. Output may be incomplete. Check the response source before repeating the command."
	for _, tc := range []struct {
		name, path, body, items string
		args                    []string
		zero, invalid           bool
	}{
		{
			name: "organization certificate activation", path: "/organization/certificates/activate",
			body: `{"certificate_ids":["cert_a","cert_b"]}`, items: first + "," + second,
			args: []string{"admin:organization:certificates", "activate", "--certificate-id", "cert_a", "--certificate-id", "cert_b"},
		},
		{
			name: "project certificate deactivation", path: "/organization/projects/proj_synthetic/certificates/deactivate",
			body: `{"certificate_ids":["cert_a","cert_b"]}`, items: first + "," + second,
			args: []string{"admin:organization:projects:certificates", "deactivate", "proj_synthetic", "--certificate-id", "cert_a", "--certificate-id", "cert_b"},
		},
		{
			name: "checkpoint permission creation", path: "/fine_tuning/checkpoints/ft_synthetic/permissions",
			body: `{"project_ids":["proj_a","proj_b"]}`, items: first + "," + second,
			args: []string{"fine-tuning:checkpoints:permissions", "create", "ft_synthetic", "--project-id", "proj_a", "--project-id", "proj_b"},
		},
		{
			name: "zero limit still creates permissions", path: "/fine_tuning/checkpoints/ft_synthetic/permissions",
			body: `{"project_ids":["proj_a"]}`, items: first + "," + second, zero: true,
			args: []string{"fine-tuning:checkpoints:permissions", "create", "ft_synthetic", "--project-id", "proj_a", "--max-items", "0"},
		},
		{
			name: "invalid permission does not repeat mutation", path: "/fine_tuning/checkpoints/ft_synthetic/permissions",
			body: `{"project_ids":["proj_a"]}`, items: first + "," + invalid, invalid: true,
			args: []string{"fine-tuning:checkpoints:permissions", "create", "ft_synthetic", "--project-id", "proj_a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantBody := finiteJSONValue(t, tc.body)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != tc.path || r.URL.RawQuery != "" {
					t.Errorf("unexpected mutation request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer sk-fake-admin-list-test" {
					t.Error("mutation did not select the synthetic admin credential")
				}
				body, err := io.ReadAll(r.Body)
				var actualBody any
				if err != nil || json.Unmarshal(body, &actualBody) != nil || !reflect.DeepEqual(actualBody, wantBody) {
					t.Errorf("mutation body=%q error=%v; want %s", body, err, tc.body)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":false}`, tc.items)
			}))
			defer server.Close()
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-list-test",
				"OPENAI_ADMIN_KEY=sk-fake-admin-list-test", "FORCE_COLOR=0",
			}, append([]string{"openai", "--format", "json"}, tc.args...)...)
			if requests.Load() != 1 {
				t.Fatalf("mutation requests=%d; want exactly one; result=%+v", requests.Load(), got)
			}
			if tc.invalid {
				if got.code != 1 {
					t.Fatalf("invalid mutation response succeeded: %+v", got)
				}
				assertFiniteJSONListPartial(t, got.stdout, "item_a")
				actual := decodeMainStructuredError(t, "json", got.stderr)
				if !reflect.DeepEqual(actual, map[string]any{"message": failureMessage}) {
					t.Fatalf("mutation failure guidance=%#v", actual)
				}
				for _, private := range []string{"synthetic-private-permission", "synthetic-private-value", "\xff"} {
					if strings.Contains(got.stdout+got.stderr, private) {
						t.Fatalf("invalid mutation response exposed fixture content %q", private)
					}
				}
				return
			}
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("mutation failed: %+v", got)
			}
			want := "[" + tc.items + "]"
			if tc.zero {
				want = "[]"
			}
			finiteJSONListRecords(t, got.stdout)
			if !reflect.DeepEqual(finiteJSONValue(t, got.stdout), finiteJSONValue(t, want)) {
				t.Fatalf("mutation output=%q; want %s", got.stdout, want)
			}
		})
	}
}

func TestMainDispatchFiniteJSONListPreservesUsageDecisionsEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, data string
	}{
		{"populated", `[{"object":"bucket","start_time":0,"end_time":60,"results":[{"object":"organization.usage.decisions.result","num_model_requests":2}]}]`},
		{"empty", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const page = "synthetic_page+cursor/="
			envelope := `{"object":"page","data":` + tc.data + `,"has_more":true,"next_page":"synthetic_next","unknown":{"large":9007199254740993,"text":"資料-é"}}`
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/organization/usage/decisions" {
					t.Errorf("unexpected usage request: %s %s", r.Method, r.URL)
				}
				if r.URL.Query().Get("page") != page || r.URL.Query().Get("start_time") != "0" {
					t.Errorf("manual usage page changed: %s", r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer sk-fake-admin-list-test" {
					t.Error("usage did not select the synthetic admin credential")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, envelope)
			}))
			defer server.Close()
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-list-test",
				"OPENAI_ADMIN_KEY=sk-fake-admin-list-test", "FORCE_COLOR=0",
			}, "openai", "--format", "json", "admin:organization:usage", "decisions", "--start-time", "0", "--page", page)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("usage response=%+v requests=%d; want one manual page", got, requests.Load())
			}
			if !json.Valid([]byte(got.stdout)) || !reflect.DeepEqual(finiteJSONValue(t, got.stdout), finiteJSONValue(t, envelope)) {
				t.Fatalf("usage response envelope changed: %q", got.stdout)
			}
		})
	}
}
