package custom

import (
	"context"
	"github.com/urfave/cli/v3"
	"io"
	"strings"
	"testing"
)

func TestBaseURLSecurityWithoutMain(t *testing.T) {
	for _, source := range []string{"flag", "environment"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv("OPENAI_BASE_URL", "http://remote.example/v1")
			called := false
			root := &cli.Command{
				Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "base-url"}},
				Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
					Name: "list", Action: func(context.Context, *cli.Command) error {
						called = true
						return nil
					},
				}}}},
			}
			ConfigureCommand(root)
			args := []string{"openai"}
			if source == "flag" {
				args = append(args, "--base-url", "http://192.0.2.1/v1")
			}
			err := root.Run(t.Context(), append(args, "models", "list"))
			if err == nil || called {
				t.Errorf("direct command execution bypassed URL policy: called=%v, error=%v", called, err)
			}
			if got := localErrorMessage(root, err); !strings.Contains(got, "HTTPS") {
				t.Errorf("missing actionable URL guidance: %q", got)
			}
		})
	}
}
