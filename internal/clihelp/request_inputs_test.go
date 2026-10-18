package clihelp_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func TestDescribeRequestInputsPreservesDeclarations(t *testing.T) {
	model := &requestflag.Flag[string]{Name: "model", Required: true, PathParam: "model"}
	project := &requestflag.Flag[string]{Name: "project-id", PathParam: "project_id", Usage: "Keep this authored constraint."}
	beforeModel, beforeProject := *model, *project
	action := func(context.Context, *cli.Command) error { return nil }
	operation := &cli.Command{Name: "retrieve", Flags: []cli.Flag{project, model}, Action: action}
	handwritten := &cli.Command{Name: "local", Flags: []cli.Flag{model}, Action: action}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{
		{Name: "models", Category: "API RESOURCE", Commands: []*cli.Command{operation}}, handwritten,
	}}
	clihelp.DescribeRequestInputs(root)
	clihelp.DescribeRequestInputs(root)
	if !reflect.DeepEqual(*model, beforeModel) || !reflect.DeepEqual(*project, beforeProject) {
		t.Fatal("presentation changed a request flag")
	}
	if got := operation.Metadata["help-positional-flags"]; !reflect.DeepEqual(got, []string{"project-id", "model"}) {
		t.Fatalf("positional declaration order changed: %v", got)
	}
	usage := operation.Metadata["help-flag-usages"].([]clihelp.FlagUsage)
	if len(usage) != 1 || usage[0].Owner != operation || usage[0].Usage != "Model ID. Use models list to find available model IDs." {
		t.Fatalf("authored or missing descriptions changed incorrectly: %+v", usage)
	}
	if handwritten.Metadata["help-positional-flags"] != nil {
		t.Fatal("URL metadata alone invented a handwritten positional argument")
	}
}

func TestDescribeRequestInputsSurvivesResourceCloning(t *testing.T) {
	for _, path := range [][]string{{"admin:projects", "retrieve"}, {"admin", "projects", "retrieve"}} {
		var out bytes.Buffer
		flag := &requestflag.Flag[string]{Name: "project-id", Required: true, PathParam: "project_id"}
		operation := &cli.Command{Name: "retrieve", Flags: []cli.Flag{flag}, Action: func(context.Context, *cli.Command) error {
			t.Fatal("help ran the request action")
			return nil
		}}
		root := &cli.Command{Name: "openai", Writer: &out, Commands: []*cli.Command{
			{Name: "admin:projects", Category: "API RESOURCE", Commands: []*cli.Command{operation}},
		}}
		clihelp.DescribeRequestInputs(root)
		localcommands.ConfigureSubgroups(root)
		args := append(append([]string{"openai"}, path...), "--help")
		args, _, err := custom.ConfigureHelp(root, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"[PROJECT_ID | --project-id PROJECT_ID]", "ID of the project.", "JSON/YAML keys: project_id."} {
			if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), want) {
				t.Errorf("route %q lost %q: %s", path, want, out.String())
			}
		}
	}
}
