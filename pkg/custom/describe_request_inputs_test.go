package custom

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
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
	describeRequestInputs(root)
	describeRequestInputs(root)
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
		describeRequestInputs(root)
		configureCommandSubgroups(root)
		args := append(append([]string{"openai"}, path...), "--help")
		args, _, err := ConfigureHelp(root, args)
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

func TestConfigureHelpImageLabelsAreOwnerScoped(t *testing.T) {
	imageSize := &requestflag.Flag[string]{Name: "size"}
	imageQuality := &requestflag.Flag[string]{Name: "quality"}
	otherSize := &requestflag.Flag[string]{Name: "size"}
	image := &cli.Command{Name: "generate", Flags: []cli.Flag{imageSize, imageQuality}}
	other := &cli.Command{Name: "other", Flags: []cli.Flag{otherSize}}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{image}}, other}}
	configureHelpGroups(root)
	configureHelpGroups(root)
	labels := image.Metadata["help-flag-labels"].([]clihelp.FlagLabel)
	for name, want := range map[string]string{"size": "SIZE", "quality": "QUALITY"} {
		count := 0
		for _, label := range labels {
			if label.Owner == image && reflect.DeepEqual(label.Names, []string{name}) && label.Label == want {
				count++
			}
		}
		if count != 1 {
			t.Errorf("image label %s appears %d times", name, count)
		}
	}
	for _, label := range other.Metadata["help-flag-labels"].([]clihelp.FlagLabel) {
		if label.Owner == other {
			t.Fatal("image label leaked to an unrelated command")
		}
	}
}
