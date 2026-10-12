package custom

import (
	"testing"

	"github.com/urfave/cli/v3"
)

func TestCommandPresentationPreservesUnknownAndAuthoredGroups(t *testing.T) {
	action := &cli.Command{Name: "create", Usage: "Create an image."}
	images := &cli.Command{Name: "images", Category: "API RESOURCE", Usage: "Authored image summary.", Commands: []*cli.Command{action}}
	future := &cli.Command{Name: "future-resources:entries", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list"}}}
	root := &cli.Command{Commands: []*cli.Command{images, future}}
	configureCommandSubgroups(root)
	configureCommandPresentation(root)
	configureCommandPresentation(root)
	if images.Usage != "Authored image summary." || action.Usage != "Create an image." || images.Category != "API RESOURCE" {
		t.Fatal("presentation replaced authored text or parser metadata")
	}
	group := root.Command("future-resources")
	if group == nil || group.Hidden || group.Usage == "" || group.Command("entries") == nil || group.Command("entries").Usage == "" {
		t.Fatal("new generated groups must remain visible and described")
	}
	if group.Metadata["help-command-section"] != "Other API resources" || !future.Hidden || len(root.Commands) != 3 {
		t.Fatal("unknown-group fallback or repeated configuration changed discovery")
	}
	if group.Command("entries").Usage != future.Usage {
		t.Fatal("unknown canonical and compatibility groups need the same fallback description")
	}
}

func TestCommandPresentationDecoratesCanonicalAndCompatibilityGroups(t *testing.T) {
	legacy := &cli.Command{Name: "admin:organization:projects:users", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list"}}}
	root := &cli.Command{Commands: []*cli.Command{legacy}}
	configureCommandSubgroups(root)
	configureCommandPresentation(root)
	canonical := root.Command("admin").Command("organization").Command("projects").Command("users")
	if canonical.Usage == "" || canonical.Usage != legacy.Usage {
		t.Fatalf("canonical and compatibility help differ: %q / %q", canonical.Usage, legacy.Usage)
	}
	if canonical.Metadata["help-command-section"] != "Access" || canonical.Hidden || !legacy.Hidden {
		t.Fatal("presentation changed route visibility or omitted access grouping")
	}
}
