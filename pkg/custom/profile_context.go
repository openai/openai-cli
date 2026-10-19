package custom

import (
	"context"
	"github.com/openai/openai-cli/internal/clihelp"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

const productionProfileURL = "https://api.openai.com/v1/"

// Keep the snapshot on the declared root flag, never in process-global state.
// PreParse clears it when a command tree is reused for another invocation.
type profileSelectorFlag struct {
	*cli.StringFlag
	ctx      context.Context
	resolved *resolvedRequestContext
	err      error
}

func (f *profileSelectorFlag) PreParse() error {
	f.ctx, f.resolved, f.err = nil, nil, nil
	// urfave retains presence and duplicate counters across PreParse calls.
	// This selector has no environment sources or caller-owned destination.
	f.StringFlag = &cli.StringFlag{Name: f.Name, Usage: f.Usage}
	return f.StringFlag.PreParse()
}

func configureProfileContext(root *cli.Command) {
	flag := &profileSelectorFlag{StringFlag: &cli.StringFlag{
		Name: "profile", Usage: "Use a saved nonsecret profile; empty disables the saved default (OPENAI_PROFILE)",
	}}
	root.Flags = append(root.Flags, flag)
	previous := root.Before
	root.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
		if previous != nil {
			var err error
			ctx, err = previous(ctx, command)
			if err != nil {
				return ctx, err
			}
		}
		flag.ctx = ctx
		return ctx, nil
	}
}

func rootProfileSelector(root *cli.Command) *profileSelectorFlag {
	for _, flag := range root.Flags {
		if selected, ok := flag.(*profileSelectorFlag); ok {
			return selected
		}
	}
	return nil
}

func selectedProfileName(root *cli.Command, lookup func(string) (string, bool), registry profileRegistry) (string, error) {
	var name string
	if root.IsSet("profile") {
		name = root.String("profile")
	} else if value, present := lookup("OPENAI_PROFILE"); present {
		name = value
	} else if registry.DefaultProfile != nil {
		name = *registry.DefaultProfile
	}
	if name != "" {
		if _, exists := registry.Profiles[name]; !exists {
			return "", &localcommands.LocalError{Message: "Selected profile does not exist. Use " + clihelp.DisplayInvocation(root) + " profile list or create the profile first."}
		}
	}
	return name, nil
}

func resolveSelectedProfile(ctx context.Context, root *cli.Command, lookup func(string) (string, bool)) (string, profileValues, error) {
	if rootProfileSelector(root) == nil {
		return "", profileValues{}, nil
	}
	selector, selected := lookup("OPENAI_PROFILE")
	if root.IsSet("profile") {
		selector, selected = root.String("profile"), true
	}
	if selected && selector == "" {
		return "", profileValues{}, nil
	}
	path, err := profileStorePath()
	if err != nil && !selected {
		// Services without a user configuration directory retain SDK defaults.
		return "", profileValues{}, nil
	}
	if err != nil {
		return "", profileValues{}, &localcommands.LocalError{Message: "Could not locate saved profiles. Check your user configuration directory.", Cause: err}
	}
	registry, err := loadProfiles(ctx, path)
	if err != nil {
		return "", profileValues{}, &localcommands.LocalError{Message: "Could not read saved profiles. Existing profiles were kept.", Cause: err}
	}
	name, err := selectedProfileName(root, lookup, registry)
	return name, registry.Profiles[name], err
}

// The same snapshot and presence rules drive requests and offline status.
func snapshotContextEnvironment(root *cli.Command, lookup func(string) (string, bool)) map[string]authStatusValue {
	names := []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_BASE_URL", "OPENAI_CUSTOM_HEADERS"}
	if rootProfileSelector(root) != nil {
		names = append(names, "OPENAI_PROFILE")
	}
	environment := make(map[string]authStatusValue, len(names))
	for _, name := range names {
		if value, present := lookup(name); present {
			environment[name] = authStatusValue{Value: value, Source: "environment:" + name}
		}
	}
	return environment
}

func requestContextSetting(root *cli.Command, environment map[string]authStatusValue, flag, variable string) authStatusValue {
	value := authStatusValue{Source: "unset"}
	if env, present := environment[variable]; present {
		value = env
	}
	for _, declared := range root.Flags {
		if explicit, ok := declared.(interface{ ExplicitRequestFlag() bool }); ok &&
			slices.Contains(declared.Names(), flag) && explicit.ExplicitRequestFlag() {
			return authStatusValue{Value: root.String(flag), Source: "flag:--" + flag}
		}
	}
	if root.IsSet(flag) {
		value.Value = root.String(flag)
		if value.Source == "unset" {
			value.Source = "flag:--" + flag
		}
	}
	return value
}

func inheritedContextHeaders(environment map[string]authStatusValue) http.Header {
	headers := make(http.Header)
	for _, line := range strings.Split(environment["OPENAI_CUSTOM_HEADERS"].Value, "\n") {
		if name, value, found := strings.Cut(line, ":"); found {
			headers.Set(strings.TrimSpace(name), strings.TrimSpace(value))
		}
	}
	return headers
}

type resolvedRequestContext struct {
	Organization authStatusValue
	Project      authStatusValue
	BaseURL      authStatusValue
}

func resolveRequestContext(ctx context.Context, root *cli.Command, environment map[string]authStatusValue, literal http.Header) (resolvedRequestContext, error) {
	lookup := func(name string) (string, bool) { value, ok := environment[name]; return value.Value, ok }
	name, profile, err := resolveSelectedProfile(ctx, root, lookup)
	if err != nil {
		return resolvedRequestContext{}, err
	}
	inherited := inheritedContextHeaders(environment)
	field := func(flag, variable, header string, saved *string) authStatusValue {
		if literal.Values(header) != nil {
			return authStatusValue{Value: literal.Get(header), Source: "flag:--header"}
		}
		value := requestContextSetting(root, environment, flag, variable)
		if value.Source != "unset" {
			return value
		}
		if saved != nil {
			return authStatusValue{Value: *saved, Source: "profile:" + name}
		}
		return value
	}
	result := resolvedRequestContext{
		Organization: field("organization", "OPENAI_ORG_ID", "OpenAI-Organization", profile.Organization),
		Project:      field("project", "OPENAI_PROJECT_ID", "OpenAI-Project", profile.Project),
		BaseURL:      authStatusValue{Value: productionProfileURL, Source: "default"},
	}
	if result.Project.Source == "unset" {
		project, err := resolveFolderProject(ctx, root)
		if err != nil {
			return resolvedRequestContext{}, err
		}
		if project != "" {
			result.Project = authStatusValue{Value: project, Source: "folder"}
		}
	}
	for header, value := range map[string]*authStatusValue{"OpenAI-Organization": &result.Organization, "OpenAI-Project": &result.Project} {
		if value.Source == "unset" && inherited.Values(header) != nil {
			*value = authStatusValue{Value: inherited.Get(header), Source: "environment:OPENAI_CUSTOM_HEADERS"}
		}
	}
	if profile.BaseURL != nil {
		value := *profile.BaseURL
		if value == "" {
			value = productionProfileURL
		} else if parsed, err := url.Parse(value); err == nil && parsed.Path != "" && !strings.HasSuffix(parsed.Path, "/") {
			parsed.Path += "/"
			value = parsed.String()
		}
		result.BaseURL = authStatusValue{Value: value, Source: "profile:" + name}
	}
	if env, present := environment["OPENAI_BASE_URL"]; present {
		result.BaseURL = env
	}
	if value := root.String("base-url"); value != "" {
		result.BaseURL = authStatusValue{Value: value, Source: "flag:--base-url"}
	}
	return result, nil
}

func requestResolutionContext(root *cli.Command) context.Context {
	if selected := rootProfileSelector(root); selected != nil && selected.ctx != nil {
		return selected.ctx
	}
	for _, flag := range root.Flags {
		if project, ok := flag.(*projectLinkFlag); ok && project.context != nil {
			return project.context
		}
	}
	return context.Background()
}

func resolvedCommandRequestContext(command *cli.Command) (resolvedRequestContext, error) {
	root := command.Root()
	selected := rootProfileSelector(root)
	var result resolvedRequestContext
	var err error
	if selected != nil && selected.resolved != nil {
		result, err = *selected.resolved, selected.err
	} else {
		var literal http.Header
		literal, err = requestconfig.Headers(root)
		if err == nil {
			result, err = resolveRequestContext(requestResolutionContext(root), root, snapshotContextEnvironment(root, os.LookupEnv), literal)
		}
		if selected != nil {
			selected.resolved, selected.err = &result, err
		}
	}
	return result, err
}

func resolvedRequestOptions(command *cli.Command) []option.RequestOption {
	result, err := resolvedCommandRequestContext(command)
	if err != nil {
		return []option.RequestOption{option.WithMaxRetries(0), option.WithMiddleware(func(*http.Request, option.MiddlewareNext) (*http.Response, error) { return nil, err })}
	}
	var options []option.RequestOption
	if result.Organization.Source != "unset" {
		options = append(options, option.WithOrganization(result.Organization.Value))
	}
	if result.Project.Source != "unset" {
		options = append(options, option.WithProject(result.Project.Value))
	}
	if strings.HasPrefix(result.BaseURL.Source, "profile:") || result.BaseURL.Source == "flag:--base-url" {
		options = append(options, option.WithBaseURL(result.BaseURL.Value))
	}
	return options
}
