package custom

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestHelpImageUsageErrorPreservesOriginalError(t *testing.T) {
	var output bytes.Buffer
	command := &cli.Command{Name: "openai", ErrWriter: &output, Metadata: map[string]any{"help-invocation": "./openai"}}
	original := cli.Exit("unexpected\n\x1b[2Joption", 17)
	got := imageGenerateUsageError(context.Background(), command, original, false)
	if got != original || got.(cli.ExitCoder).ExitCode() != 17 {
		t.Fatalf("usage handler changed original error or exit code: %v", got)
	}
	if strings.Contains(output.String(), "\x1b") || !strings.Contains(output.String(), `\x1b`) || !strings.Contains(output.String(), `unexpected\n`) {
		t.Fatalf("unexpected parser details were not safely quoted: %q", output.String())
	}
	if !strings.Contains(output.String(), "./openai images generate --help") {
		t.Fatalf("usage handler lost local invocation: %q", output.String())
	}
}
