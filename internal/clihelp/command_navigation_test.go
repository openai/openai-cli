package clihelp

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestVisibleCommandsKeepsParserOrderAndFutureCommands(t *testing.T) {
	run := func(context.Context, *cli.Command) error { return nil }
	root := &cli.Command{Commands: []*cli.Command{
		{Name: "future-group", Category: "API RESOURCE"},
		{Name: "second-group", Category: "API RESOURCE", Metadata: map[string]any{"help-command-rank": 2}},
		{Name: "first-action", Action: run},
		{Name: "first-group", Category: "API RESOURCE", Metadata: map[string]any{"help-command-rank": 1}},
		{Name: "second-action", Action: run},
		{Name: "legacy:path", Hidden: true},
	}}
	before := slices.Clone(root.Commands)
	var names []string
	for _, command := range VisibleCommands(root) {
		names = append(names, command.Name)
	}
	want := []string{"first-action", "second-action", "first-group", "second-group", "future-group"}
	if !slices.Equal(names, want) || !slices.Equal(root.Commands, before) {
		t.Fatalf("visible order=%q; wanted %q without mutating the parser tree", names, want)
	}
}

func TestVisibleCommandsPreservesCuratedOrder(t *testing.T) {
	root := &cli.Command{
		Metadata: map[string]any{"help-preserve-command-order": true},
		Commands: []*cli.Command{
			{Name: "generate"},
			{Name: "inline", Commands: []*cli.Command{{Name: "enable"}}},
			{Name: "create-variation"},
			{Name: "legacy:path", Hidden: true},
		},
	}
	var names []string
	for _, command := range VisibleCommands(root) {
		names = append(names, command.Name)
	}
	if want := []string{"generate", "inline", "create-variation"}; !slices.Equal(names, want) {
		t.Fatalf("curated command order=%q; want %q", names, want)
	}
}

func TestHelpCommandMapIsCompleteAndConsistent(t *testing.T) {
	var brief, full string
	for _, all := range []bool{false, true} {
		var out bytes.Buffer
		group := &cli.Command{Name: "resources", Usage: "Manage resources."}
		for i := range 12 {
			group.Commands = append(group.Commands, &cli.Command{Name: fmt.Sprintf("item-%02d", i), Usage: "Use this item."})
		}
		group.Commands = append(group.Commands, &cli.Command{Name: "hidden", Hidden: true})
		root := &cli.Command{Name: "openai", Writer: &out, HideHelpCommand: true, Commands: []*cli.Command{group}}
		args := []string{"openai", "resources", "--help"}
		if all {
			args = []string{"openai", "help", "--all", "resources"}
		}
		args, _, err := Configure(root, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
		for i := range 12 {
			if strings.Count(out.String(), fmt.Sprintf("item-%02d", i)) != 1 {
				t.Errorf("full=%v: immediate command missing or repeated: %s", all, out.String())
			}
		}
		if strings.Contains(out.String(), "hidden") || strings.Contains(out.String(), "more in full help") {
			t.Errorf("command map exposed hidden names or hid immediate commands: %s", out.String())
		}
		if all {
			full = out.String()
		} else {
			brief = out.String()
		}
	}
	for _, got := range []string{brief, full} {
		if !strings.Contains(got, "ACTIONS") {
			t.Errorf("missing actions heading: %s", got)
		}
	}
}

func TestRootHelpIncludesOrderedCommandSections(t *testing.T) {
	for _, all := range []bool{false, true} {
		var out bytes.Buffer
		root := &cli.Command{Name: "openai", Writer: &out, HideHelpCommand: true, Commands: []*cli.Command{
			{Name: "files", Usage: "Manage uploaded files.", Metadata: map[string]any{"help-command-section": "Data", "help-command-rank": 20}},
			{Name: "responses", Usage: "Generate model responses.", Metadata: map[string]any{"help-command-section": "Generate", "help-command-rank": 10}},
			{Name: "legacy:path", Hidden: true},
		}}
		args := []string{"openai", "--help"}
		if all {
			args = []string{"openai", "help", "--all"}
		}
		args, _, err := Configure(root, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		for _, want := range []string{"GENERATE", "DATA", "Generate model responses.", "Manage uploaded files."} {
			if !strings.Contains(got, want) {
				t.Errorf("full=%v: missing %q: %s", all, want, got)
			}
		}
		if strings.Index(got, "GENERATE") > strings.Index(got, "DATA") || strings.Contains(got, "legacy:path") {
			t.Errorf("wrong visible section order: %s", got)
		}
	}
}

func TestMixedHelpSeparatesActionsAndCommandGroups(t *testing.T) {
	group := &cli.Command{Name: "projects", Commands: []*cli.Command{
		{Name: "users", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list"}}},
		{Name: "create", Action: func(context.Context, *cli.Command) error { return nil }},
		{Name: "retrieve", Action: func(context.Context, *cli.Command) error { return nil }},
	}}
	got := commandHelpAtWidth(group, "openai", "admin organization projects", 80)
	for _, want := range []string{"ACTIONS", "COMMAND GROUPS", "create", "retrieve", "users"} {
		if !strings.Contains(got, want) {
			t.Errorf("mixed help lacks %q: %s", want, got)
		}
	}
	if strings.Index(got, "create") > strings.Index(got, "users") {
		t.Errorf("actions must precede groups: %s", got)
	}
}

func TestBriefHelpUsesFileInputMetadata(t *testing.T) {
	for _, name := range []string{"file", "image", "mask"} {
		flag := &requestflag.Flag[string]{Name: name, Usage: "The audio file object (not file name) to transcribe.", Required: true, FileInput: true}
		got := commandHelpAtWidth(&cli.Command{Name: "create", Flags: []cli.Flag{flag}}, "openai", "audio transcriptions create", 80)
		if !strings.Contains(got, "Path to the audio file to transcribe.") || strings.Contains(got, "file object") {
			t.Errorf("file flag %s still describes an SDK input: %s", name, got)
		}
		if flag.Usage != "The audio file object (not file name) to transcribe." {
			t.Fatal("help changed the generated flag")
		}
	}
}

func TestFullHelpFileInputWordingKeepsConstraints(t *testing.T) {
	for _, tc := range []struct{ usage, want string }{
		{"The File object (not file name) to be uploaded. Maximum size 10 MiB.", "Path to the file to upload."},
		{"The audio file object (not file name) to transcribe, in these formats: mp3, wav. Maximum size 10 MiB.", "Path to the audio file to transcribe"},
		{"The audio file object (not file name) translate, in these formats: mp3, wav. Maximum size 10 MiB.", "Path to the audio file to translate"},
	} {
		for _, fileInput := range []bool{false, true} {
			flag := &requestflag.Flag[string]{Name: "file", Usage: tc.usage, FileInput: fileInput}
			if err := flag.PreParse(); err != nil {
				t.Fatal(err)
			}
			got := fullFlag(flag)
			want := tc.usage
			if fileInput {
				want = tc.want
			}
			if !strings.Contains(got, want) || !strings.Contains(got, "Maximum size 10 MiB.") {
				t.Errorf("FileInput=%v lost wording or constraints: %s", fileInput, got)
			}
			if strings.Contains(tc.usage, "mp3, wav") && !strings.Contains(got, "mp3, wav") {
				t.Errorf("file formats were lost: %s", got)
			}
			if flag.Usage != tc.usage {
				t.Fatal("full help modified the generated definition")
			}
		}
	}
}
