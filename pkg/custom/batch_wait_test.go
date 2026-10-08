package custom

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchWaitResult(t *testing.T) {
	for _, tc := range []struct {
		status   string
		counts   string
		terminal bool
		failure  bool
	}{
		{"validating", `{}`, false, false},
		{"in_progress", `{}`, false, false},
		{"finalizing", `{}`, false, false},
		{"cancelling", `{}`, false, false},
		{"completed", `{"total":3,"completed":3,"failed":0}`, true, false},
		{"completed", `{"total":3,"completed":2,"failed":1}`, true, true},
		{"completed", `{}`, true, true},
		{"completed", `{"failed":null}`, true, true},
		{"completed", `{"failed":-1}`, true, true},
		{"completed", `{"total":3,"completed":2,"failed":0}`, true, true},
		{"completed", `{"total":0,"completed":0,"failed":0}`, true, false},
		{"failed", `{}`, true, true},
		{"expired", `{}`, true, true},
		{"cancelled", `{}`, true, true},
		{"future_status", `{}`, true, true},
		{"", `{}`, true, true},
	} {
		t.Run(tc.status+tc.counts, func(t *testing.T) {
			batch := gjson.Parse(`{"status":"` + tc.status + `","request_counts":` + tc.counts + `}`)
			terminal, err := batchWaitResult(batch)
			if terminal != tc.terminal || (err != nil) != tc.failure {
				t.Fatalf("got terminal=%v err=%v", terminal, err)
			}
		})
	}
}

func TestBatchHintRetainsOnlyReproducibleRequestContext(t *testing.T) {
	for _, tc := range []struct {
		name, flag, environment, inherited string
		args                               []string
		needsOptions                       bool
	}{
		{"environment key", "api-key", "OPENAI_API_KEY", "synthetic-env-key", nil, false},
		{"matching key", "api-key", "OPENAI_API_KEY", "synthetic-env-key", []string{"--api-key", "synthetic-env-key"}, false},
		{"different key", "api-key", "OPENAI_API_KEY", "synthetic-env-key", []string{"--api-key", "synthetic-override-key"}, true},
		{"empty key override", "api-key", "OPENAI_API_KEY", "synthetic-env-key", []string{"--api-key="}, true},
		{"project", "project", "OPENAI_PROJECT_ID", "proj_env", []string{"--project", "proj_override"}, true},
		{"organization", "organization", "OPENAI_ORG_ID", "org_env", []string{"--organization", "org_override"}, true},
		{"admin key", "admin-api-key", "OPENAI_ADMIN_KEY", "", []string{"--admin-api-key", "synthetic-admin-key"}, true},
		{"secret", "webhook-secret", "OPENAI_WEBHOOK_SECRET", "", []string{"--webhook-secret", "synthetic-secret"}, true},
		{"certificate", mtlsClientCertFileFlag, mtlsClientCertFileEnv, "", []string{"--mtls-client-cert-file", "private-cert.pem"}, true},
		{"client key", mtlsClientKeyFileFlag, mtlsClientKeyFileEnv, "", []string{"--mtls-client-key-file", "private-key.pem"}, true},
		{"header", "unused", "OPENAI_CUSTOM_HEADERS", "X-Example: inherited", []string{"--header", "X-Example: override"}, true},
		{"environment headers", "unused", "OPENAI_CUSTOM_HEADERS", "X-Example: inherited", nil, false},
		{"environment endpoint", "unused", "OPENAI_BASE_URL", "http://127.0.0.1:1", nil, false},
		{"explicit endpoint", "unused", "OPENAI_BASE_URL", "http://127.0.0.1:1", []string{"--base-url", "http://127.0.0.1:1"}, true},
		{"different endpoint", "unused", "OPENAI_BASE_URL", "http://127.0.0.1:1", []string{"--base-url", "http://127.0.0.1:2"}, true},
		{"empty endpoint fallback", "unused", "OPENAI_BASE_URL", "http://127.0.0.1:1", []string{"--base-url="}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.environment, tc.inherited)
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{
					&requestflag.Flag[string]{Name: tc.flag, Sources: cli.EnvVars(tc.environment)},
					&cli.StringFlag{Name: "base-url"}, NewRequestHeaderFlag(),
				},
				Action: func(_ context.Context, cmd *cli.Command) error {
					if got := batchHintNeedsRequestOptions(cmd); got != tc.needsOptions {
						t.Errorf("needs options = %v; want %v", got, tc.needsOptions)
					}
					return nil
				},
			}
			if err := root.Run(t.Context(), append([]string{"openai"}, tc.args...)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBatchProgressUsesOnlyValidReturnedCounts(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"status":"in_progress","request_counts":{"completed":415,"failed":5,"total":1000}}`, "Processing: 420 of 1000 requests finished."},
		{`{"status":"completed","request_counts":{"completed":995,"failed":5,"total":1000}}`, "Completed: 995 succeeded, 5 failed."},
		{`{"status":"completed","request_counts":{"completed":2,"failed":0,"total":3}}`, "Batch status: completed"},
		{`{"status":"in_progress"}`, "Batch status: in_progress"},
		{`{"status":"in_progress","request_counts":{"completed":3,"failed":1,"total":3}}`, "Batch status: in_progress"},
		{`{"status":"in_progress","request_counts":{"completed":-1,"failed":1,"total":3}}`, "Batch status: in_progress"},
		{`{"status":"in_progress","request_counts":{"completed":18446744073709551615,"failed":1,"total":18446744073709551615}}`, "Batch status: in_progress"},
		{`{"status":"in_progress","request_counts":{"completed":"1","failed":0,"total":3}}`, "Batch status: in_progress"},
	} {
		if got := batchProgress(gjson.Parse(tc.body)); got != tc.want {
			t.Errorf("got %q; want %q", got, tc.want)
		}
	}
}

func TestBatchContextErrorsPreserveCausesAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  int
	}{{context.Canceled, 130}, {context.DeadlineExceeded, 124}} {
		err := batchContextError(tc.cause)
		var exit cli.ExitCoder
		if !errors.Is(err, tc.cause) || !errors.As(err, &exit) || exit.ExitCode() != tc.code {
			t.Fatalf("cause/code lost: %v", err)
		}
	}
	other := errors.New("transport failure")
	if batchContextError(other) != other {
		t.Fatal("changed unrelated failure")
	}
}

func TestBatchWaitOptInPreservesGeneratedAction(t *testing.T) {
	for _, flags := range [][]string{nil, {"--wait=false"}} {
		called := 0
		root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
			Commands: []*cli.Command{{Name: "batches", Commands: []*cli.Command{{Name: "retrieve", Action: func(context.Context, *cli.Command) error {
				called++
				return nil
			}}}}},
		}
		configureBatchCommands(root)
		if err := root.Run(t.Context(), append([]string{"openai", "batches", "retrieve"}, flags...)); err != nil {
			t.Fatal(err)
		}
		if called != 1 {
			t.Fatalf("generated action calls=%d", called)
		}
	}
}
