package custom

import (
	"bytes"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

func TestAdminSetupRepeatedHelpConfiguration(t *testing.T) {
	for _, name := range []string{"OPENAI_ADMIN_KEY", "OPENAI_CUSTOM_HEADERS", "OPENAI_BASE_URL"} {
		t.Setenv(name, "")
	}
	root := &cli.Command{Name: "openai"}
	for _, executable := range []string{"openai", "./a user's directory/openai"} {
		args := []string{executable, "help", "setup", "admin"}
		if _, handled, err := ConfigureHelp(root, args); err != nil || !handled {
			t.Fatalf("ConfigureHelp = %t, %v", handled, err)
		}
		setup := root.Command("help").Command("setup")
		if len(setup.Commands) != 1 {
			t.Fatalf("repeated configuration registered %d setup commands", len(setup.Commands))
		}
		var output bytes.Buffer
		cli.HelpPrinter(&output, setup.Command("admin").CustomHelpTemplate, root)
		invocation := clihelp.Invocation(root.Name, args)
		if !strings.Contains(output.String(), invocation+" --format text admin organization projects list") {
			t.Fatalf("guide lost current invocation %q: %s", invocation, &output)
		}
		root.Metadata["help-invocation"] = invocation
		err := checkAdminCredentials(root)
		if err == nil || !strings.Contains(err.Error(), invocation+" help setup admin") {
			t.Fatalf("missing-key error lost current invocation %q: %v", invocation, err)
		}
	}
}
