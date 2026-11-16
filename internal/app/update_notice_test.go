package app

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestUpdateNoticeInvocationGate(t *testing.T) {
	for _, args := range [][]string{
		nil, {""}, {"openai", "--"}, {"openai", "--help"},
		{"openai", "help"}, {"openai", "--version"}, {"openai", "__complete"},
		{"openai", "--format", "json"}, {"openai", "--quiet"},
		{"openai", "--quiet=false"}, {"openai", "models"},
		{"openai", "responses", "create"}, {"openai", "images", "generate"},
	} {
		if updateNoticeEnvironment(args, func(string) string { return "" }) {
			t.Errorf("non-bare invocation admitted: %q", args)
		}
	}
	for _, executable := range []string{"openai", "/path with spaces/openai", `C:\tools\openai.exe`} {
		if !updateNoticeEnvironment([]string{executable}, func(string) string { return "" }) {
			t.Errorf("bare invocation rejected: %q", executable)
		}
	}
}

func TestUpdateNoticeEnvironmentGate(t *testing.T) {
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "TF_BUILD", "BUILDKITE", "JENKINS_URL", "TEAMCITY_VERSION", "OPENAI_CLI_NO_UPDATE_CHECK"} {
		for _, value := range []string{"1", "true", "TRUE", "yes", "anything"} {
			getenv := func(key string) string {
				if key == name {
					return value
				}
				return ""
			}
			if updateNoticeEnvironment([]string{"openai"}, getenv) {
				t.Errorf("admitted %s=%s", name, value)
			}
		}
	}
	for _, value := range []string{"dumb", "DUMB"} {
		if updateNoticeEnvironment([]string{"openai"}, func(key string) string {
			if key == "TERM" {
				return value
			}
			return ""
		}) {
			t.Errorf("admitted TERM=%s", value)
		}
	}
	for _, value := range []string{"", "0", "false", "FALSE"} {
		if !updateNoticeEnvironment([]string{"openai"}, func(key string) string {
			if key == "OPENAI_CLI_NO_UPDATE_CHECK" {
				return value
			}
			return ""
		}) {
			t.Errorf("rejected disabled opt-out %q", value)
		}
	}
}

func TestUpdateNoticeRedirectedStreamsNeverCheck(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	var out, diagnostic bytes.Buffer
	root := &cli.Command{Reader: input, Writer: &out, ErrWriter: &diagnostic, Version: "1.0.0"}
	called := false
	showUpdateNotice(context.Background(), []string{"openai"}, root, func(context.Context, string) string {
		called = true
		return "Update available"
	})
	if called || out.Len() != 0 || diagnostic.Len() != 0 {
		t.Fatalf("redirected invocation performed optional work: called=%v out=%q err=%q", called, out.String(), diagnostic.String())
	}
}

func TestUpdateNoticeCanceledInvocationNeverChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	showUpdateNotice(ctx, []string{"openai"}, &cli.Command{}, func(context.Context, string) string {
		t.Fatal("canceled invocation checked for updates")
		return ""
	})
}
