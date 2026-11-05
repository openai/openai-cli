package request

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/profiles"
	"github.com/urfave/cli/v3"
)

// ProjectContextError retains the cause while displaying only authored request guidance.
type ProjectContextError struct {
	message string
	cause   error
}

func (e *ProjectContextError) Error() string { return e.message }
func (e *ProjectContextError) Unwrap() error { return e.cause }

// Keep the selector local to this invocation, including when callers reuse the command tree.
type profileFlag struct{ cli.StringFlag }

func (flag *profileFlag) PreParse() error {
	flag.StringFlag = cli.StringFlag{Name: flag.Name, Usage: flag.Usage}
	return flag.StringFlag.PreParse()
}

func NewProfileFlag() cli.Flag {
	return &profileFlag{cli.StringFlag{Name: "profile", Usage: "Use a saved organization and project for this command (OPENAI_PROFILE); empty disables saved defaults"}}
}

func profileFlagRegistered(root *cli.Command) bool {
	for _, flag := range root.Flags {
		if _, ok := flag.(*profileFlag); ok {
			return true
		}
	}
	return false
}

func profileSelector(root *cli.Command, lookup func(string) (string, bool)) (string, bool) {
	if root.IsSet("profile") {
		return root.String("profile"), true
	}
	return lookup("OPENAI_PROFILE")
}

func selectedRegistryName(registry profiles.Registry, name string, explicit bool) (string, error) {
	if !explicit && registry.DefaultProfile != nil {
		name = *registry.DefaultProfile
	}
	if _, exists := registry.Profiles[name]; name != "" && !exists {
		return "", &ProjectContextError{message: "Selected profile does not exist. Use profile list to inspect saved names."}
	}
	return name, nil
}

func SelectedProfile(ctx context.Context, root *cli.Command, lookup func(string) (string, bool)) (string, profiles.Values, error) {
	if !profileFlagRegistered(root) {
		return "", profiles.Values{}, nil
	}
	name, explicit := profileSelector(root, lookup)
	if explicit && name == "" {
		return "", profiles.Values{}, nil
	}
	path, err := profiles.Path()
	if err != nil && !explicit {
		return "", profiles.Values{}, nil
	}
	if err != nil {
		return "", profiles.Values{}, &ProjectContextError{message: "Could not locate profiles.json. Check your user configuration directory.", cause: err}
	}
	registry, err := profiles.Load(ctx, path)
	if err != nil {
		return "", profiles.Values{}, &ProjectContextError{message: "Could not read profiles.json. Check the file in your user configuration directory. Use --profile= to bypass saved profiles for one command.", cause: err}
	}
	name, err = selectedRegistryName(registry, name, explicit)
	values := registry.Profiles[name]
	// Even an empty legacy setting explicitly selected production in the earlier draft.
	if values.BaseURL != nil {
		return "", profiles.Values{}, &ProjectContextError{message: "This profile contains a legacy base URL. Use profile set NAME --unset base_url, then specify --base-url or OPENAI_BASE_URL for requests."}
	}
	return name, values, err
}

type ContextValue struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

type ProjectContext struct{ Organization, Project ContextValue }

func EnvironmentHeaders(raw string) http.Header {
	headers := make(http.Header)
	for line := range strings.SplitSeq(raw, "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok {
			headers.Set(strings.TrimSpace(name), strings.TrimSpace(value))
		}
	}
	return headers
}

func ResolveProjectContext(root *cli.Command, lookup func(string) (string, bool), literal, inherited http.Header, name string, profile profiles.Values) ProjectContext {
	field := func(flag, variable, header string, saved *string) ContextValue {
		if literal.Values(header) != nil {
			return ContextValue{literal.Get(header), "flag:--header"}
		}
		if ExplicitRootFlag(root, flag) {
			return ContextValue{root.String(flag), "flag:--" + flag}
		}
		value, present := lookup(variable)
		if root.IsSet(flag) && (root.String(flag) != "" || present) {
			return ContextValue{root.String(flag), "environment:" + variable}
		}
		if present {
			return ContextValue{value, "environment:" + variable}
		}
		if saved != nil {
			return ContextValue{*saved, "profile:" + name}
		}
		if inherited.Values(header) != nil {
			return ContextValue{inherited.Get(header), "environment:OPENAI_CUSTOM_HEADERS"}
		}
		return ContextValue{Source: "unset"}
	}
	return ProjectContext{
		Organization: field("organization", "OPENAI_ORG_ID", "OpenAI-Organization", profile.Organization),
		Project:      field("project", "OPENAI_PROJECT_ID", "OpenAI-Project", profile.Project),
	}
}

const projectContextMetadata = "openai-request-project-context"

// ConfiguredProjectContext returns the same request snapshot used on the wire.
func ConfiguredProjectContext(root *cli.Command) (ProjectContext, bool) {
	selection, configured := root.Metadata[projectContextMetadata].(ProjectContext)
	return selection, configured
}

// Resolve once after flag parsing. All requests within a command use the same selected profile.
func ConfigureProjectContext(ctx context.Context, root *cli.Command) error {
	delete(root.Metadata, projectContextMetadata)
	name, values, err := SelectedProfile(ctx, root, os.LookupEnv)
	if err != nil {
		return err
	}
	literal, err := Headers(root)
	if err != nil {
		return &ProjectContextError{message: "Invalid --" + err.Error() + ".", cause: err}
	}
	selection := ResolveProjectContext(root, os.LookupEnv, literal, EnvironmentHeaders(os.Getenv("OPENAI_CUSTOM_HEADERS")), name, values)
	if name != "" {
		// The current credentials may differ from those present when the profile was saved.
		secrets := ConfiguredCredentials(root, os.LookupEnv)
		for _, field := range []ContextValue{selection.Organization, selection.Project} {
			if field.Source == "profile:"+name && ContainsCredential(field.Value, secrets) {
				return &ProjectContextError{message: "A saved organization or project contains a configured credential. Use profile set to replace the value, or --profile= to bypass saved settings."}
			}
		}
	}
	root.Metadata[projectContextMetadata] = selection
	return nil
}
