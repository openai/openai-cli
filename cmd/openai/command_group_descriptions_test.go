package main

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/urfave/cli/v3"
)

func TestMainCommandGroupsHaveWorkflowDescriptions(t *testing.T) {
	count := 0
	var visit func(*cli.Command, string)
	visit = func(command *cli.Command, path string) {
		if command.Category == "API RESOURCE" {
			count++
			if strings.TrimSpace(command.Usage) == "" || command.Metadata["help-fallback-description"] == true {
				t.Errorf("current API group needs an authored workflow description: %s", path)
			}
		}
		for _, child := range command.Commands {
			if !child.Hidden {
				visit(child, strings.TrimSpace(path+" "+child.Name))
			}
		}
	}
	visit(cmd.Command, "")
	if count < 90 {
		t.Fatalf("expected the complete group tree, inspected %d", count)
	}
	for _, legacy := range cmd.Command.Commands {
		if !legacy.Hidden || legacy.Category != "API RESOURCE" || !strings.Contains(legacy.Name, ":") {
			continue
		}
		canonical := cmd.Command
		for _, part := range strings.Split(legacy.Name, ":") {
			canonical = canonical.Command(part)
			if canonical == nil {
				t.Fatalf("missing canonical path for %s", legacy.Name)
			}
		}
		if canonical.Usage != legacy.Usage {
			t.Errorf("compatibility description differs for %s", legacy.Name)
		}
	}
	t.Logf("verified authored descriptions across %d API resource and namespace groups", count)
}
