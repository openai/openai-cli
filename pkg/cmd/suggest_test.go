package cmd

import (
	"testing"

	"github.com/urfave/cli/v3"
)

func TestSuggestCommandSkipsUnrelatedInput(t *testing.T) {
	commands := []*cli.Command{
		{Name: "completions"},
		{Name: "responses"},
	}

	if got := suggestCommand(commands, "totallybogus"); got != "" {
		t.Fatalf("expected no suggestion for unrelated input, got %q", got)
	}
}

func TestSuggestCommandKeepsCloseMatches(t *testing.T) {
	commands := []*cli.Command{
		{Name: "create"},
		{Name: "delete"},
	}

	if got := suggestCommand(commands, "creat"); got != "Did you mean 'create'?" {
		t.Fatalf("expected create suggestion, got %q", got)
	}
}
