package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// Track argv separately from PostParse: request flags count environment values
// as sets too. Inspecting a link must never save an ambient project selection.
type projectLinkFlag struct {
	*rootRequestFlag
	explicit bool
	context  context.Context
}

func (f *projectLinkFlag) Set(name, value string) error {
	if err := f.rootRequestFlag.Set(name, value); err != nil {
		return err
	}
	f.explicit = true
	return nil
}

func registerProjectLinkCommands(root *cli.Command) {
	for i, flag := range root.Flags {
		if project, ok := flag.(*rootRequestFlag); ok && project.Name == "project" {
			root.Flags[i] = &projectLinkFlag{rootRequestFlag: project}
		}
	}
	previousBefore := root.Before
	root.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
		if previousBefore != nil {
			var err error
			ctx, err = previousBefore(ctx, command)
			if err != nil {
				return ctx, err
			}
		}
		for _, flag := range root.Flags {
			if project, ok := flag.(*projectLinkFlag); ok {
				project.context = ctx
			}
		}
		return ctx, nil
	}
	for _, name := range []string{"link", "unlink"} {
		usage, example := "Inspect or save this folder's OpenAI project", "link --project proj_work"
		description := "With --project, save a project ID for this folder. Without it, inspect the saved link and effective project.\n" +
			"Flags override OPENAI_PROJECT_ID, which overrides the nearest linked parent folder. Empty overrides disable folder defaults.\n" +
			"No API request or credentials are needed. Linking does not grant project access or change your API key."
		if name == "unlink" {
			usage, example = "Remove this folder's saved OpenAI project", "unlink"
			description = "Remove only this folder's link. An inherited parent link remains active.\n" +
				"No API request is made. This does not delete a remote project, remote files, or local files."
		}
		description += "\nSupports --format auto, text, or json. No positional arguments, --transform, or --raw-output."
		help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} ` + example + "\n\n" + cli.CommandHelpTemplate + localUtilityGlobalHelp
		root.Commands = append(root.Commands, &cli.Command{
			Name: name, Usage: usage, Description: description, HideHelpCommand: true,
			CustomHelpTemplate: help,
			Metadata:           map[string]any{localUtilityMetadata: true, "local-help-full": help, "help-command-section": "Local tools"},
			Action:             handleProjectLink,
		})
	}
}

func explicitProjectLinkFlag(root *cli.Command) bool {
	for _, flag := range root.Flags {
		if project, ok := flag.(*projectLinkFlag); ok {
			return project.explicit
		}
	}
	return false
}

func projectLinkPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(directory) {
		return "", errors.New("folder links require an absolute user configuration directory")
	}
	return filepath.Join(directory, "openai", "project-links.json"), nil
}

func projectLinkDirectory() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	return filepath.Clean(directory), nil
}

func validLinkedProject(project string) bool {
	suffix, ok := strings.CutPrefix(project, "proj_")
	if !ok || suffix == "" {
		return false
	}
	for _, c := range suffix {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func nearestProjectLink(links map[string]string, directory string) (string, string) {
	for {
		if project, ok := links[directory]; ok {
			return directory, project
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", ""
		}
		directory = parent
	}
}

// No environment mutation: clients retain API keys and their actual access.
// Root ownership prevents endpoint body fields named project from changing this.
func projectLinkRequestOptions(command *cli.Command) []option.RequestOption {
	root := command.Root()
	if root.IsSet("project") {
		return []option.RequestOption{option.WithProject(root.String("project"))}
	}
	if project, present := os.LookupEnv("OPENAI_PROJECT_ID"); present {
		return []option.RequestOption{option.WithProject(project)}
	}
	path, err := projectLinkPath()
	if err != nil {
		// A process without a user configuration location has no folder defaults.
		// Preserve the existing API behavior for minimal service environments.
		return nil
	}
	// Production callers use the context captured by the root Before hook.
	// Direct option-only callers retain a context without a cancellation signal.
	ctx := context.Background()
	for _, flag := range root.Flags {
		if project, ok := flag.(*projectLinkFlag); ok && project.context != nil {
			ctx = project.context
		}
	}
	links, err := loadProjectLinks(ctx, path)
	if err == nil && len(links) == 0 {
		return nil
	}
	var directory string
	if err == nil {
		directory, err = projectLinkDirectory()
	}
	if err != nil {
		failure := projectLinkFailure("Could not resolve folder project defaults. Inspect with openai link, or override with --project.", err)
		return []option.RequestOption{option.WithMaxRetries(0), option.WithMiddleware(func(*http.Request, option.MiddlewareNext) (*http.Response, error) {
			return nil, failure
		})}
	}
	_, project := nearestProjectLink(links, directory)
	if project == "" {
		return nil
	}
	return []option.RequestOption{option.WithProject(project)}
}

type projectLinkStatus struct {
	Action           string `json:"action"`
	Directory        string `json:"directory"`
	LinkedDirectory  string `json:"linked_directory"`
	Project          string `json:"project"`
	Inherited        bool   `json:"inherited"`
	EffectiveProject string `json:"effective_project"`
	Source           string `json:"source"`
	ProjectRedacted  bool   `json:"effective_project_redacted,omitempty"`
}

func handleProjectLink(ctx context.Context, command *cli.Command) error {
	root := command.Root()
	format := strings.ToLower(root.String("format"))
	if command.Args().Present() || root.IsSet("transform") || root.IsSet("raw-output") {
		return projectLinkFailure("Folder linking takes no positional arguments and does not support --transform or --raw-output.", nil)
	}
	if format != "" && format != "auto" && format != "text" && format != "json" {
		return projectLinkFailure("Folder linking supports --format auto, text, or json.", nil)
	}
	explicit := explicitProjectLinkFlag(root)
	if command.Name == "unlink" && explicit {
		return projectLinkFailure("unlink does not accept --project. It removes only the current folder's saved link.", nil)
	}
	project := root.String("project")
	if command.Name == "link" && explicit && !validLinkedProject(project) {
		return projectLinkFailure("Use --project with a project ID starting with proj_ and containing only letters, digits, underscores, or hyphens.", nil)
	}
	directory, err := projectLinkDirectory()
	if err != nil {
		return projectLinkFailure("Could not find the current folder. Change to an existing accessible folder and try again.", err)
	}
	path, err := projectLinkPath()
	if err != nil {
		return projectLinkFailure("Could not find your configuration folder. Check HOME, APPDATA, or XDG_CONFIG_HOME.", err)
	}
	status := projectLinkStatus{Action: "inspect", Directory: directory}
	var links map[string]string
	switch {
	case command.Name == "unlink":
		status.Action = "unlink"
		links, err = updateProjectLink(ctx, path, directory, "")
	case explicit:
		status.Action = "link"
		links, err = updateProjectLink(ctx, path, directory, project)
	default:
		links, err = loadProjectLinks(ctx, path)
	}
	if err != nil {
		return projectLinkFailure("Could not read or save folder links. Check the user configuration folder's permissions and project-links.json. Invalid settings were kept.", err)
	}
	status.LinkedDirectory, status.Project = nearestProjectLink(links, directory)
	status.Inherited = status.LinkedDirectory != "" && status.LinkedDirectory != directory
	status.EffectiveProject, status.Source = status.Project, "folder"
	if status.Project == "" {
		status.Source = "default"
	}
	if environment, present := os.LookupEnv("OPENAI_PROJECT_ID"); present {
		status.EffectiveProject, status.Source = environment, "environment"
		if environment != "" && !validLinkedProject(environment) {
			status.EffectiveProject, status.ProjectRedacted = "", true
		}
	}
	// The --project on link saves a future default; this invocation has no API
	// request. Show the environment override that future commands retain.
	if err := ctx.Err(); err != nil {
		return err
	}
	writer := outputWriter{ctx: ctx, out: root.Writer}
	if format == "json" {
		err = json.NewEncoder(writer).Encode(status)
	} else if status.Action == "inspect" || !quietOutput(ctx) {
		_, err = io.WriteString(writer, projectLinkText(status))
	}
	if err != nil {
		return projectLinkFailure("Could not write folder-link output. The saved link may already have changed; inspect with openai link.", err)
	}
	return nil
}

func projectLinkText(status projectLinkStatus) string {
	var out strings.Builder
	switch status.Action {
	case "link":
		if status.Source == "folder" {
			fmt.Fprintf(&out, "This folder now uses OpenAI project %s.\n", readable.Text(status.Project))
		} else {
			fmt.Fprintf(&out, "Saved OpenAI project %s for this folder.\n", readable.Text(status.Project))
		}
	case "unlink":
		out.WriteString("This folder has no saved project link.\n")
	}
	if status.Project != "" && status.Action != "link" {
		fmt.Fprintf(&out, "Folder project: %s\n", readable.Text(status.Project))
		if status.Inherited {
			fmt.Fprintf(&out, "Inherited from: %s\n", readable.Text(status.LinkedDirectory))
		}
	} else if status.Action == "inspect" {
		out.WriteString("This folder has no saved project link.\n")
	}
	if status.Source == "environment" {
		if status.ProjectRedacted {
			out.WriteString("OPENAI_PROJECT_ID overrides the folder default. Its value is not a project ID and is hidden.\n")
		} else if status.EffectiveProject == "" {
			out.WriteString("OPENAI_PROJECT_ID is empty and disables the folder default.\n")
		} else {
			fmt.Fprintf(&out, "Effective project: %s (OPENAI_PROJECT_ID overrides the folder default)\n", readable.Text(status.EffectiveProject))
		}
	}
	out.WriteString("Your API key and its project access stay unchanged.\n")
	return out.String()
}

func projectLinkFailure(message string, cause error) error {
	return &localUtilityError{message: message, cause: cause}
}
