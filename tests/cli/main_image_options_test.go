package cli_test

import (
	"strings"
	"testing"
)

func TestMainImageOptionsReadOnlyRoutes(t *testing.T) {
	// These are help commands, so even broken request configuration must not
	// prevent users from learning the next command. No API key is needed.
	env := []string{"OPENAI_BASE_URL=not-a-request-url", "OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem", "OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem"}
	for _, topic := range []string{"", "model", "size", "quality", "count", "format", "background", "moderation", "partials", "upload", "save"} {
		t.Run(topic, func(t *testing.T) {
			args := []string{"./openai", "images", "options"}
			if topic != "" {
				args = append(args, topic)
			}
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || got.stdout == "" {
				t.Fatalf("settings guide = %+v", got)
			}
			withHelp := runMainDispatchWithEnv(t, "bash", env, append(args, "--help")...)
			if withHelp != got {
				t.Fatalf("implicit and explicit guide differ: %+v / %+v", got, withHelp)
			}
			if !strings.Contains(got.stdout, "./openai images ") || strings.Contains(got.stdout, "{{") {
				t.Fatalf("guide has no runnable examples or unrendered template: %s", got.stdout)
			}
			if topic != "" && (strings.Count(got.stdout, "\n") > 12 || !strings.Contains(got.stdout, "More details:")) {
				t.Fatalf("topic should fit on one short screen with a details link: %s", got.stdout)
			}
			full := runMainDispatchWithEnv(t, "bash", env, append(args, "--all")...)
			if full.code != 0 || full.stderr != "" || full.stdout == got.stdout {
				t.Fatalf("--all did not reveal more information without request setup: %+v", full)
			}
			rootHelp := append([]string{"./openai", "help", "--all"}, args[1:]...)
			if viaRoot := runMainDispatchWithEnv(t, "bash", env, rootHelp...); viaRoot != full {
				t.Fatalf("root full help differs from --all: %+v / %+v", viaRoot, full)
			}
			beforeTopic := []string{"./openai", "images", "options", "--all"}
			if topic != "" {
				beforeTopic = append(beforeTopic, topic)
			}
			if before := runMainDispatchWithEnv(t, "bash", env, beforeTopic...); before != full {
				t.Fatalf("--all before topic differs: %+v / %+v", before, full)
			}
		})
	}
}

func TestMainImageOptionsExplainsCompatibility(t *testing.T) {
	for _, tc := range []struct {
		topic string
		want  []string
	}{
		{"", []string{"gpt-image-2.5-sunburst", "--count 2", "--partial-images 2", "images options size", "command only"}},
		{"count", []string{"--count 10", "-n 2", "one final image", "dall-e-3"}},
		{"quality", []string{"--quality xhigh", "--quality max", "Older models differ"}},
		{"format", []string{"--output-format jpeg", "--format json", "instead of saving files"}},
		{"background", []string{"--background transparent", "PNG", "JPEG cannot"}},
		{"partials", []string{"up to", "--partial-images 0", "automatically", "--count 1", "API-event output does not save"}},
		{"upload", []string{"images edit --image ./robot.png", "new image saves to ~/Downloads/gpt-images/", "Variations also save automatically", "--format json for full API data", "help --all images edit", "images preview ./robot.png"}},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			args := []string{"./openai", "images", "options"}
			if tc.topic != "" {
				args = append(args, tc.topic)
			}
			args = append(args, "--all")
			got := runMainDispatch(t, "bash", args...)
			if got.code != 0 {
				t.Fatalf("guide failed: %+v", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(strings.ToLower(got.stdout), strings.ToLower(want)) {
					t.Errorf("guide missing %q: %s", want, got.stdout)
				}
			}
		})
	}
}

func TestMainImageOptionsUnknownTopic(t *testing.T) {
	got := runMainDispatch(t, "bash", "./openai", "images", "options", "unknown")
	if got.code == 0 || !strings.Contains(got.stderr, "images options") {
		t.Fatalf("unknown topic must give a useful error: %+v", got)
	}
}
