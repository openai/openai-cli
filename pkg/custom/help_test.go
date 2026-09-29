package custom

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

func TestConfigureHelpRefreshesImageExampleInvocation(t *testing.T) {
	images := &cli.Command{Name: "images", Commands: []*cli.Command{
		{Name: "generate"}, {Name: "edit"}, {Name: "create-variation"},
		{Name: "unrelated", Description: "    openai unrelated"},
	}}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{images}}
	originals := map[string]string{
		"generate": imageGenerationSavingHelp, "edit": imageEditSavingHelp,
		"create-variation": imageVariationSavingHelp,
	}
	// The same tree can be configured again by an embedding caller or a test.
	for _, executable := range []string{"./openai", "./a user's directory/openai", "openai"} {
		args := []string{executable, "help", "--all", "images", "generate"}
		if _, _, err := ConfigureHelp(root, args); err != nil {
			t.Fatal(err)
		}
		invocation := clihelp.Invocation(root.Name, args)
		for name, original := range originals {
			want := strings.Replace(original, "\n    openai ", "\n    "+invocation+" ", 1)
			if got := images.Command(name).Description; got != want {
				t.Errorf("%s example after %q: got %q, want %q", name, executable, got, want)
			}
		}
		if got := images.Command("unrelated").Description; got != "    openai unrelated" {
			t.Fatalf("unrelated description changed: %q", got)
		}
	}
}
