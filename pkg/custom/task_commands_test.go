package custom

import (
	"context"
	"io"
	"testing"

	"github.com/urfave/cli/v3"
)

func taskCommandTree() *cli.Command {
	return &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{
		{Name: "audio:transcriptions", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "create"}}},
		{Name: "admin:organization:projects", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list"}}},
		{Name: "admin:organization:users", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list"}}},
	}}
}

func TestTaskCommandsPreserveIdentityAndIndependentNodes(t *testing.T) {
	for _, path := range [][]string{{"audio:transcriptions", "create"}, {"audio", "transcriptions", "create"}, {"audio", "transcribe"}, {"transcribe"}} {
		root := taskCommandTree()
		var selected string
		root.Commands[0].Commands[0].Action = func(_ context.Context, command *cli.Command) error {
			selected = commandResourceName(command)
			return nil
		}
		ConfigureCommand(root)
		count := len(root.Commands)
		ConfigureCommand(root)
		if len(root.Commands) != count {
			t.Fatal("repeated configuration duplicated routes")
		}
		seen := map[*cli.Command]bool{}
		var visit func(*cli.Command)
		visit = func(command *cli.Command) {
			if seen[command] {
				t.Fatalf("shared mutable command node: %s", command.Name)
			}
			seen[command] = true
			for _, child := range command.Commands {
				visit(child)
			}
		}
		visit(root)
		root.Command("transcribe").Metadata["probe"] = true
		if root.Command("audio").Command("transcribe").Metadata["probe"] != nil {
			t.Fatal("shortcut shares metadata with primary command")
		}
		if err := root.Run(context.Background(), append([]string{"openai"}, path...)); err != nil {
			t.Fatal(err)
		}
		if selected != "audio:transcriptions" {
			t.Fatalf("resource identity changed for %v: %q", path, selected)
		}
	}
}

func TestTaskCommandsPreserveFutureCommandsAndCollisions(t *testing.T) {
	for _, name := range []string{"group-name", "group-alias", "root-alias", "future-audio-action", "admin-name", "admin-alias", "root-projects"} {
		t.Run(name, func(t *testing.T) {
			root := taskCommandTree()
			configureCommandSubgroups(root)
			audio := root.Command("audio")
			transcriptions := audio.Command("transcriptions")
			admin := root.Command("admin")
			organization := admin.Command("organization")
			reserved := &cli.Command{Name: "reserved"}
			switch name {
			case "group-name":
				reserved.Name = "transcribe"
				audio.Commands = append(audio.Commands, reserved)
			case "group-alias":
				reserved.Aliases = []string{"transcribe"}
				audio.Commands = append(audio.Commands, reserved)
			case "root-alias":
				reserved.Aliases = []string{"transcribe"}
				root.Commands = append(root.Commands, reserved)
			case "future-audio-action":
				transcriptions.Commands = append(transcriptions.Commands, reserved)
			case "admin-name":
				reserved.Name = "projects"
				admin.Commands = append(admin.Commands, reserved)
			case "admin-alias":
				reserved.Aliases = []string{"projects"}
				admin.Commands = append(admin.Commands, reserved)
			case "root-projects":
				reserved.Name = "projects"
				root.Commands = append(root.Commands, reserved)
			}
			configureTaskCommands(root)
			switch name {
			case "group-name", "group-alias":
				if audio.Command("transcribe") != reserved || transcriptions.Hidden || root.Command("transcribe") != nil {
					t.Fatal("task route replaced an existing command or hid its source")
				}
			case "root-alias":
				if root.Command("transcribe") != reserved || audio.Command("transcribe") == nil {
					t.Fatal("shortcut collision changed the primary command")
				}
			case "future-audio-action":
				if transcriptions.Hidden || transcriptions.Command("reserved") != reserved || audio.Command("transcribe") == nil {
					t.Fatal("task route hid a future generated action")
				}
			case "admin-name", "admin-alias":
				if organization.Hidden || admin.Command("projects") != reserved || admin.Command("users") == nil || root.Command("projects") != nil {
					t.Fatal("partial promotion hid or replaced a generated route")
				}
			case "root-projects":
				if root.Command("projects") != reserved || admin.Command("projects") == nil {
					t.Fatal("project shortcut replaced a generated route")
				}
			}
		})
	}
}

func TestTaskCommandsNeverSkipAncestorBehavior(t *testing.T) {
	for _, ancestor := range []string{"transcriptions", "audio", "organization", "admin"} {
		for _, behavior := range []string{"flag", "before", "after", "action", "validator", "skip-parsing", "short-options", "stop-parsing"} {
			t.Run(ancestor+"/"+behavior, func(t *testing.T) {
				root := taskCommandTree()
				configureCommandSubgroups(root)
				audio, admin := root.Command("audio"), root.Command("admin")
				organization := admin.Command("organization")
				command := map[string]*cli.Command{"transcriptions": audio.Command("transcriptions"), "audio": audio, "organization": organization, "admin": admin}[ancestor]
				switch behavior {
				case "flag":
					command.Flags = []cli.Flag{&cli.StringFlag{Name: "future-option"}}
				case "before":
					command.Before = func(ctx context.Context, _ *cli.Command) (context.Context, error) { return ctx, nil }
				case "after":
					command.After = func(context.Context, *cli.Command) error { return nil }
				case "action":
					command.Action = func(context.Context, *cli.Command) error { return nil }
				case "validator":
					command.ArgValidator = func(context.Context, *cli.Command) error { return nil }
				case "skip-parsing":
					command.SkipFlagParsing = true
				case "short-options":
					command.UseShortOptionHandling = true
				case "stop-parsing":
					value := 1
					command.StopOnNthArg = &value
				}
				configureTaskCommands(root)
				switch ancestor {
				case "transcriptions":
					if audio.Command("transcribe") != nil || command.Hidden {
						t.Fatal("skipped transcription group behavior")
					}
				case "audio":
					if root.Command("transcribe") != nil || audio.Command("transcribe") == nil {
						t.Fatal("skipped audio group behavior")
					}
				case "organization":
					if admin.Command("projects") != nil || command.Hidden {
						t.Fatal("skipped organization behavior")
					}
				case "admin":
					if root.Command("projects") != nil || admin.Command("projects") == nil {
						t.Fatal("skipped admin behavior")
					}
				}
			})
		}
	}
}
