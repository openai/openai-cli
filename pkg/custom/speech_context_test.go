package custom

import (
	"context"
	"testing"

	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/urfave/cli/v3"
)

const speechStreamOperation = "(resource) audio.speech > (method) create"

// Obtain feature context through its installed action; private context keys stay owned by audio.
func testSpeechContext(t *testing.T, ctx context.Context, opts ShowJSONOpts) context.Context {
	t.Helper()
	var captured context.Context
	leaf := &cli.Command{Name: "create", Action: func(ctx context.Context, _ *cli.Command) error {
		captured = ctx
		return nil
	}}
	root := &cli.Command{Commands: []*cli.Command{{Name: "audio:speech", Commands: []*cli.Command{leaf}}}}
	audio.ConfigureSpeech(root, func(context.Context, *cli.Command) audio.SpeechPresentation { return speechPresentation(opts) })
	if err := leaf.Action(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	if captured == nil {
		t.Fatal("speech action did not capture request context")
	}
	return captured
}
