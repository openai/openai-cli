package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestRequestConfigurationRestoresEnvironment(t *testing.T) {
	const original = "not%url"
	for _, outcome := range []string{"success", "setup error", "action error", "cancellation", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			t.Setenv("OPENAI_BASE_URL", original)
			unchanged := map[string]string{
				"OPENAI_API_KEY":               "synthetic-key",
				"OPENAI_ADMIN_KEY":             "synthetic-admin-key",
				"OPENAI_ORG_ID":                "org-synthetic",
				"OPENAI_PROJECT_ID":            "proj-synthetic",
				"OPENAI_WEBHOOK_SECRET":        "synthetic-webhook-secret",
				"OPENAI_CUSTOM_HEADERS":        "X-Synthetic: preserved",
				"OPENAI_MTLS_CLIENT_CERT_FILE": "/synthetic/cert.pem",
				"OPENAI_MTLS_CLIENT_KEY_FILE":  "/synthetic/key.pem",
			}
			for key, value := range unchanged {
				t.Setenv(key, value)
			}
			for attempt := range 2 {
				endpoint := fmt.Sprintf("http://127.0.0.1:%d", attempt+1)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := cli.Exit("synthetic failure", 17)
				checkActive := func() {
					t.Helper()
					if got := os.Getenv("OPENAI_BASE_URL"); got != endpoint {
						t.Errorf("effective URL = %q, want %q", got, endpoint)
					}
					for key, value := range unchanged {
						if os.Getenv(key) != value {
							t.Errorf("changed unrelated environment variable %s", key)
						}
					}
				}
				app := &cli.Command{
					Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
					Flags:          []cli.Flag{&cli.StringFlag{Name: "base-url"}},
					ExitErrHandler: func(context.Context, *cli.Command, error) {},
				}
				app.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
					checkActive()
					if outcome == "setup error" {
						return ctx, failure
					}
					return ctx, nil
				}
				before := reflect.ValueOf(app.Before).Pointer()
				afterCalled := false
				app.After = func(context.Context, *cli.Command) error {
					afterCalled = true
					checkActive()
					return nil
				}
				app.Action = func(context.Context, *cli.Command) error {
					checkActive()
					switch outcome {
					case "action error":
						return failure
					case "cancellation":
						cancel()
						return ctx.Err()
					case "panic":
						panic("synthetic panic")
					}
					child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRequestConfigurationChildEnvironment$")
					child.Env = append(os.Environ(), "OPENAI_CLI_URL_ENV_CHILD=1")
					output, err := child.Output()
					if err != nil || string(output) != endpoint {
						t.Errorf("child did not inherit the effective URL: %q, %v", output, err)
					}
					return nil
				}
				var gotErr error
				var gotPanic any
				func() {
					defer func() { gotPanic = recover() }()
					gotErr = runWithRequestConfiguration(ctx, app, []string{"openai", "--base-url", endpoint}, false)
				}()
				switch outcome {
				case "setup error", "action error":
					if gotErr != failure {
						t.Errorf("changed error identity: got %v, want %v", gotErr, failure)
					}
				case "cancellation":
					if gotErr != context.Canceled {
						t.Errorf("changed cancellation error: %v", gotErr)
					}
				default:
					if gotErr != nil {
						t.Errorf("unexpected error: %v", gotErr)
					}
				}
				if outcome == "panic" && gotPanic != "synthetic panic" || outcome != "panic" && gotPanic != nil {
					t.Errorf("unexpected panic: %v", gotPanic)
				}
				if outcome == "success" && !afterCalled {
					t.Error("After hook did not run")
				}
				if value, present := os.LookupEnv("OPENAI_BASE_URL"); !present || value != original {
					t.Errorf("environment not restored: %q, present=%v", value, present)
				}
				if reflect.ValueOf(app.Before).Pointer() != before {
					t.Error("Before hook not restored")
				}
				for key, value := range unchanged {
					if os.Getenv(key) != value {
						t.Errorf("changed unrelated environment variable %s", key)
					}
				}
			}
		})
	}
}

func TestRequestConfigurationChildEnvironment(t *testing.T) {
	if os.Getenv("OPENAI_CLI_URL_ENV_CHILD") != "1" {
		return
	}
	fmt.Print(os.Getenv("OPENAI_BASE_URL"))
	os.Exit(0)
}

func TestRequestConfigurationLeavesOtherEnvironmentCasesUnchanged(t *testing.T) {
	for _, test := range []struct {
		name, ambient                         string
		unset, noFlag, emptyFlag, wantFailure bool
	}{
		{name: "valid environment", ambient: "http://127.0.0.1:2"},
		{name: "empty environment"},
		{name: "unset environment", unset: true},
		{name: "omitted override", ambient: "not%url", noFlag: true, wantFailure: true},
		{name: "empty override", ambient: "not%url", emptyFlag: true, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OPENAI_BASE_URL", test.ambient)
			if test.unset {
				if err := os.Unsetenv("OPENAI_BASE_URL"); err != nil {
					t.Fatal(err)
				}
			}
			app := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "base-url"}},
				Action: func(context.Context, *cli.Command) error {
					if value, present := os.LookupEnv("OPENAI_BASE_URL"); value != test.ambient || present == test.unset {
						t.Errorf("unnecessary environment change: %q, present=%v", value, present)
					}
					return nil
				}}
			args := []string{"openai"}
			if !test.noFlag {
				value := "http://127.0.0.1:1"
				if test.emptyFlag {
					value = ""
				}
				args = append(args, "--base-url", value)
			}
			err := runWithRequestConfiguration(t.Context(), app, args, false)
			if (err != nil) != test.wantFailure {
				t.Errorf("error=%v, want failure=%v", err, test.wantFailure)
			}
			if value, present := os.LookupEnv("OPENAI_BASE_URL"); value != test.ambient || present == test.unset {
				t.Errorf("environment not preserved: %q, present=%v", value, present)
			}
		})
	}
}
