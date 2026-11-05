package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/profiles"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

// These command-local flags have no sources, destinations, or nonzero defaults.
// Fresh declarations clear urfave's retained presence and duplicate counters.
type profileStringFlag struct{ cli.StringFlag }

func (f *profileStringFlag) PreParse() error {
	f.StringFlag = cli.StringFlag{Name: f.Name, Usage: f.Usage, OnlyOnce: true}
	return f.StringFlag.PreParse()
}

type profileUnsetFlag struct{ cli.StringSliceFlag }

func (f *profileUnsetFlag) PreParse() error {
	f.StringSliceFlag = cli.StringSliceFlag{Name: f.Name, Usage: f.Usage}
	return f.StringSliceFlag.PreParse()
}

type profileDeactivateFlag struct{ cli.BoolFlag }

func (f *profileDeactivateFlag) PreParse() error {
	f.BoolFlag = cli.BoolFlag{Name: f.Name, Usage: f.Usage, OnlyOnce: true}
	return f.BoolFlag.PreParse()
}

func RegisterProfiles(root *cli.Command) {
	if root.Command("profile") != nil {
		return
	}
	const overview = "Profiles store nonsecret settings only. No command contacts the API or changes credentials or permissions. " +
		"Management commands ignore --profile and OPENAI_PROFILE. Active means the saved default."
	const local = "Works locally without API requests. Supports --format auto, text, json, or jsonl."
	const edit = "Only flags declared by profile set edit saved fields. Root request flags and environment values never persist. " +
		"An empty value remains present; --unset removes presence. Supports --format auto, text, json, or jsonl."
	profile := profileCommand("profile", "Manage saved organization and project settings locally", overview, "profile create work")
	profile.Commands = []*cli.Command{
		profileCommand("create", "Create an empty profile without activating it", local, "profile create work"),
		profileCommand("set", "Edit only explicit saved profile fields", edit, "profile set work --project proj_demo"),
		profileCommand("get", "Show one saved profile", local, "profile get work"),
		profileCommand("list", "List saved profiles in name order", local, "profile list"),
		profileCommand("activate", "Select a saved default profile", local, "profile activate work"),
		profileCommand("delete", "Delete a saved profile", local, "profile delete work --deactivate"),
	}
	for _, command := range profile.Commands {
		command.ArgsUsage = "NAME"
		command.Action = handleProfileCommand
	}
	profile.Command("list").ArgsUsage = ""
	set := profile.Command("set")
	set.DisableSliceFlagSeparator = true
	set.Flags = []cli.Flag{
		&profileStringFlag{cli.StringFlag{Name: "organization", Usage: "Save organization `VALUE`; an empty value suppresses lower defaults", OnlyOnce: true}},
		&profileStringFlag{cli.StringFlag{Name: "project", Usage: "Save project `VALUE`; an empty value suppresses lower defaults", OnlyOnce: true}},
		&profileUnsetFlag{cli.StringSliceFlag{Name: "unset", Usage: "Remove saved `FIELD`: organization or project (also accepts legacy base_url); repeat for multiple fields"}},
	}
	profile.Command("delete").Flags = []cli.Flag{
		&profileDeactivateFlag{cli.BoolFlag{Name: "deactivate", Usage: "Clear the saved default when deleting its active profile", OnlyOnce: true}},
	}
	root.Commands = append(root.Commands, profile)
	if auth := root.Command("auth"); auth != nil && auth.Command("switch") == nil {
		const switchDescription = "Select a profile with root --profile, present OPENAI_PROFILE, or the saved default. " +
			"Show the saved project and the effective project for this invocation. " +
			"This does not activate the profile, contact the API, or change credentials or permissions. " +
			"Supports --format auto, text, json, or jsonl."
		command := profileCommand("switch", "Change the selected profile's saved project locally", switchDescription, "--profile work auth switch --to-project proj_demo")
		command.Flags = []cli.Flag{
			&profileStringFlag{cli.StringFlag{Name: "to-project", Usage: "Save nonempty project `VALUE` in the selected profile", OnlyOnce: true}},
		}
		command.Action = handleProfileCommand
		auth.Commands = append(auth.Commands, command)
	}
}

func profileCommand(name, usage, description, example string) *cli.Command {
	return &cli.Command{
		Name: name, Usage: usage, Description: description, HideHelpCommand: true,
		Metadata: map[string]any{
			localcommands.LocalUtilityMetadata: true, "help-command-section": "Local tools",
			"help-content": clihelp.Content{
				Description: description,
				Examples:    []clihelp.Example{{Description: usage + ".", Command: example}},
				Notes:       "No API key is required.",
			},
			"completion-root-flag-values": map[string][]string{"format": {"auto", "text", "json", "jsonl"}},
		},
	}
}

type profileRecord struct {
	Name   string          `json:"name"`
	Active bool            `json:"active"`
	Values profiles.Values `json:"values"`
}

type profileReceipt struct {
	Action           string           `json:"action"`
	Profile          profileRecord    `json:"profile"`
	EffectiveProject *authStatusValue `json:"effective_project,omitempty"`
}

type profileDeleteReceipt struct {
	Action      string `json:"action"`
	Name        string `json:"name"`
	Deactivated bool   `json:"deactivated"`
}

func handleProfileCommand(ctx context.Context, command *cli.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root := command.Root()
	format := strings.ToLower(root.String("format"))
	if root.IsSet("transform") || root.IsSet("raw-output") {
		return profileFailure("Profile commands do not support --transform or --raw-output. Use --format text, json, or jsonl.", nil)
	}
	if format != "auto" && format != "text" && format != "json" && format != "jsonl" {
		return profileFailure("Profile commands support --format auto, text, json, or jsonl.", nil)
	}
	action, name := command.Name, command.Args().First()
	if action == "list" || action == "switch" {
		if command.Args().Present() {
			return profileFailure("This profile command takes no positional arguments. Use its --help for an example.", nil)
		}
	} else if command.Args().Len() != 1 || !profiles.ValidName(name) {
		return profileFailure("Provide one nonempty profile name without controls or surrounding whitespace.", nil)
	}
	var edits map[string]*string
	if action == "set" {
		var err error
		edits, err = explicitProfileEdits(command)
		if err != nil {
			return err
		}
	}
	if action == "switch" && (!command.IsSet("to-project") || command.String("to-project") == "" || !profiles.ValidHeader(command.String("to-project"))) {
		return profileFailure("Use --to-project with a nonempty UTF-8 project value without controls. Select a profile with root --profile or activate one.", nil)
	}
	secrets := request.ConfiguredCredentials(root, os.LookupEnv)
	if action == "create" && request.ContainsCredential(name, secrets) || action == "switch" && request.ContainsCredential(command.String("to-project"), secrets) {
		return profileFailure("Use a nonsecret profile name and project ID. Credentials cannot be saved in profiles.", nil)
	}
	for _, v := range edits {
		if v != nil && request.ContainsCredential(*v, secrets) {
			return profileFailure("Use a nonsecret organization or project ID. Credentials cannot be saved in profiles.", nil)
		}
	}
	path, err := profiles.Path()
	if err != nil {
		return profileFailure("Could not find your configuration folder. Check HOME, APPDATA, or XDG_CONFIG_HOME.", err)
	}
	var result any
	committed := action != "get" && action != "list"
	if !committed {
		registry, err := profiles.Load(ctx, path)
		if err != nil {
			return profileCommandStoreFailure(err)
		}
		if action == "list" {
			names := make([]string, 0, len(registry.Profiles))
			for name := range registry.Profiles {
				names = append(names, name)
			}
			slices.Sort(names)
			records := make([]profileRecord, 0, len(names))
			for _, name := range names {
				records = append(records, savedProfileRecord(registry, name, secrets))
			}
			result = records
		} else {
			if _, exists := registry.Profiles[name]; !exists {
				return profileFailure("That profile does not exist. Use "+clihelp.DisplayInvocation(root)+" profile list to inspect saved names.", nil)
			}
			result = savedProfileRecord(registry, name, secrets)
		}
	} else {
		deactivated := false
		var effective *authStatusValue
		registry, err := profiles.Update(ctx, path, func(registry *profiles.Registry) error {
			if action == "switch" {
				var err error
				// Resolve the saved default while holding the same lock as the edit.
				name, err = request.SelectedName(root, os.LookupEnv, *registry)
				if err != nil {
					return err
				}
				if name == "" {
					return profileFailure("No profile is selected. Use root --profile or activate a saved profile.", nil)
				}
			}
			values, exists := registry.Profiles[name]
			if action == "create" {
				if exists {
					return profileFailure("That profile already exists. Use "+clihelp.DisplayInvocation(root)+" profile set to edit its saved fields.", nil)
				}
				registry.Profiles[name] = profiles.Values{}
				return nil
			}
			if !exists {
				return profileFailure("That profile does not exist. Use "+clihelp.DisplayInvocation(root)+" profile list to inspect saved names.", nil)
			}
			switch action {
			case "set":
				for field, value := range edits {
					switch field {
					case "organization":
						values.Organization = value
					case "project":
						values.Project = value
					case "base_url":
						values.BaseURL = value
					}
				}
				registry.Profiles[name] = values
			case "activate":
				registry.DefaultProfile = &name
			case "delete":
				deactivated = registry.DefaultProfile != nil && *registry.DefaultProfile == name
				if deactivated && !command.Bool("deactivate") {
					return profileFailure("That profile is the saved default. Repeat with --deactivate to clear the default and delete it.", nil)
				}
				if deactivated {
					registry.DefaultProfile = nil
				}
				delete(registry.Profiles, name)
			case "switch":
				project := command.String("to-project")
				values.Project = &project
				if values.BaseURL != nil {
					return profileFailure("This profile contains a legacy base URL. Use profile set NAME --unset base_url before switching.", nil)
				}
				literal, err := request.Headers(root)
				if err != nil {
					return profileFailure("Invalid --"+err.Error()+".", err)
				}
				selection := request.ResolveProjectContext(root, os.LookupEnv, literal, request.EnvironmentHeaders(os.Getenv("OPENAI_CUSTOM_HEADERS")), name, values)
				field := authStatusValue(selection.Project)
				field.redact(secrets)
				effective = &field
				registry.Profiles[name] = values
			}
			return nil
		})
		if err != nil {
			return profileCommandStoreFailure(err)
		}
		if action == "delete" {
			if request.ContainsCredential(name, secrets) {
				name = "[redacted]"
			}
			result = profileDeleteReceipt{Action: action, Name: name, Deactivated: deactivated}
		} else {
			result = profileReceipt{Action: action, Profile: savedProfileRecord(registry, name, secrets), EffectiveProject: effective}
		}
	}
	if err := writeProfileReceipt(root.Writer, func(out io.Writer) error {
		return writeProfileResult(ctx, out, format, result)
	}); err != nil {
		message := "Could not write saved profile data. This command did not change saved profiles."
		if committed {
			message = "Saved profiles were updated, but the result could not be written."
		}
		failure := &localcommands.LocalError{
			Message: message + " Inspect with " + clihelp.DisplayInvocation(root) + " profile list.",
			Cause:   err,
		}
		if committed {
			return &committedProfileError{failure}
		}
		return failure
	}
	return nil
}

// Read declared local flags directly. An inherited root flag is never a saved edit.
func explicitProfileEdits(command *cli.Command) (map[string]*string, error) {
	edits := make(map[string]*string)
	for _, declared := range command.Flags {
		flag, ok := declared.(*profileStringFlag)
		if !ok || !flag.IsSet() {
			continue
		}
		field := flag.Name
		switch field {
		case "organization", "project":

		default:
			continue
		}
		value := flag.Get().(string)
		edits[field] = &value
	}
	for _, field := range command.StringSlice("unset") {
		if field != "organization" && field != "project" && field != "base_url" {
			return nil, profileFailure("Unknown --unset field. Use organization or project. To clear a legacy base URL, use --unset base_url.", nil)
		}
		if value, present := edits[field]; present && value != nil {
			return nil, profileFailure("Do not set and unset the same profile field in one command.", nil)
		}
		edits[field] = nil
	}
	if len(edits) == 0 {
		return nil, profileFailure("No saved edits were specified. Use profile set flags after the command name, or use --unset FIELD.", nil)
	}
	for _, value := range edits {
		if value != nil && !profiles.ValidHeader(*value) {
			return nil, profileFailure("Profile organization and project values must contain valid UTF-8 without controls.", nil)
		}
	}
	return edits, nil
}

func savedProfileRecord(registry profiles.Registry, name string, secrets []string) profileRecord {
	values := registry.Profiles[name]
	values.BaseURL = nil
	mask := "[redacted]"
	if values.Organization != nil && request.ContainsCredential(*values.Organization, secrets) {
		values.Organization = &mask
	}
	if values.Project != nil && request.ContainsCredential(*values.Project, secrets) {
		values.Project = &mask
	}
	record := profileRecord{Name: name, Active: registry.DefaultProfile != nil && *registry.DefaultProfile == name, Values: values}
	if request.ContainsCredential(name, secrets) {
		record.Name = mask
	}
	return record
}

func writeProfileResult(ctx context.Context, writer io.Writer, format string, result any) error {
	out := clioutput.Writer{Context: ctx, Sink: writer}
	if format == "json" || format == "jsonl" {
		encoder := json.NewEncoder(out)
		if records, ok := result.([]profileRecord); ok && format == "jsonl" {
			for _, record := range records {
				if err := encoder.Encode(record); err != nil {
					return err
				}
			}
			return ctx.Err()
		}
		return encoder.Encode(result)
	}
	var text strings.Builder
	switch value := result.(type) {
	case profileRecord:
		writeProfileText(&text, value)
	case []profileRecord:
		for index, record := range value {
			if index != 0 {
				text.WriteByte('\n')
			}
			writeProfileText(&text, record)
		}
		if len(value) == 0 {
			text.WriteString("No saved profiles.\n")
		}
	case profileReceipt:
		fmt.Fprintf(&text, "Saved profile (%s).\n", value.Action)
		writeProfileText(&text, value.Profile)
		if value.EffectiveProject != nil {
			fmt.Fprintf(&text, "Effective project: %s\n", authStatusContextText(*value.EffectiveProject))
		}
	case profileDeleteReceipt:
		fmt.Fprintf(&text, "Deleted profile: %s\n", profileTextValue(value.Name))
		if value.Deactivated {
			text.WriteString("Saved default: cleared\n")
		}
	}
	_, err := io.WriteString(out, text.String())
	return err
}

func writeProfileText(out *strings.Builder, record profileRecord) {
	active := ""
	if record.Active {
		active = " (saved default)"
	}
	fmt.Fprintf(out, "Profile: %s%s\n", profileTextValue(record.Name), active)
	for _, field := range []struct {
		label string
		value *string
	}{{"Organization", record.Values.Organization}, {"Saved project", record.Values.Project}} {
		value := "(unset)"
		if field.value != nil {
			value = profileTextValue(*field.value)
			if *field.value == "" {
				value = "(empty)"
			}
		}
		fmt.Fprintf(out, "%s: %s\n", field.label, value)
	}
}

func profileTextValue(value string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(value))
}

func profileCommandStoreFailure(cause error) error {
	var local *localcommands.LocalError
	var selection *request.ProjectContextError
	if errors.As(cause, &local) || errors.As(cause, &selection) {
		return cause
	}
	recovery := "Check profiles.json and its lock in your user configuration folder."
	switch {
	case errors.Is(cause, profiles.ErrInvalid):
		recovery = "Repair invalid JSON, fields, or default selection in profiles.json before retrying."
	case errors.Is(cause, profiles.ErrTooLarge):
		recovery = "Reduce profiles.json to at most 1 MiB before retrying."
	case errors.Is(cause, profiles.ErrChanged):
		recovery = "Stop other profile writers before retrying."
	case errors.Is(cause, profiles.ErrUnsafe):
		recovery = "Use a private regular profiles.json owned by your user, without a symlink."
	}
	return profileFailure("Could not read or update saved profiles. "+recovery+" Saved profiles were not changed.", cause)
}

func profileFailure(message string, cause error) error {
	return &localcommands.LocalError{Message: message, Cause: cause}
}

// Preserve the successful commit in diagnostics when cancellation interrupts receipt output.
type committedProfileError struct{ *localcommands.LocalError }

func (*committedProfileError) CommittedProfileChange() {}
