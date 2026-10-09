package custom

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

const adminSetupInteractiveHelp = `{{$run := or (index .Root.Metadata "help-invocation") "openai"}}Verify an admin key with hidden input
  {{$run}} setup admin

Paste your admin key at the hidden prompt, then press Enter.
The CLI checks access by listing one project. It does not change projects.
The key stays in this process. This command does not authenticate later commands.

Need a key? Organization owners can create one here:
  https://platform.openai.com/settings/organization/admin-keys
Select your organization before creating the key.
Create an admin key.
Copy the key into the hidden prompt.
If you are not an owner, ask an owner to perform the admin operation.
Do not ask the owner to share a key.

Setup requires a terminal and text output. Ctrl+C cancels.
Verification stops after 15 seconds, without retries or redirects.
The displayed destination follows --base-url or OPENAI_BASE_URL.
HTTPS is required except for a local loopback server.
The pasted key replaces the Authorization header for this request.
Debug logging and structured output are unavailable during interactive setup.

Manual setup for scripts: {{$run}} help setup admin
`

const adminSetupRootHelp = `{{$run := or (index .Root.Metadata "help-invocation") "openai"}}Set up admin access

Enter an admin key securely and verify access:
  {{$run}} setup admin

Show command details:
  {{$run}} setup admin --help

Show manual setup for an ordinary API key:
  {{$run}} help setup
`

type adminSetupError struct {
	message string
	code    int
	// Only canonical context errors belong here, never backend or caller data.
	cause error
}

func (err *adminSetupError) Error() string { return err.message }
func (err *adminSetupError) Unwrap() error { return err.cause }
func (err *adminSetupError) ExitCode() int {
	if err.code != 0 {
		return err.code
	}
	return 1
}

func registerAdminSetup(root *cli.Command) {
	setup := root.Command("setup")
	if setup == nil {
		setup = &cli.Command{
			Name: "setup", Usage: "Verify credentials interactively", HideHelpCommand: true,
			CustomHelpTemplate: adminSetupRootHelp,
		}
		root.Commands = append(root.Commands, setup)
	}
	if setup.Command("admin") == nil {
		setup.Commands = append(setup.Commands, &cli.Command{
			Name: "admin", Usage: "Enter an admin key securely and verify access",
			HideHelpCommand: true, CustomHelpTemplate: adminSetupInteractiveHelp,
			Action: handleAdminSetup,
		})
	}
}

func handleAdminSetup(ctx context.Context, command *cli.Command) error {
	root := command.Root()
	if command.Args().Present() {
		return &adminSetupError{message: "Run setup admin without extra arguments. Enter the key only at the hidden prompt.", code: 3}
	}
	if root.Bool("debug") {
		return &adminSetupError{message: "Remove --debug before running setup admin. Interactive key entry does not support debug logging.", code: 3}
	}
	if resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) != "text" || errorOutputFormat(root) != "text" ||
		root.String("transform") != "" || root.String("transform-error") != "" || root.Bool("raw-output") {
		return &adminSetupError{message: "Interactive admin setup requires text output. Use help setup admin for noninteractive instructions.", code: 3}
	}
	options, destination, err := adminSetupRequestOptions(command)
	if err != nil {
		return err
	}
	if !isTerminal(os.Stdin) || !isTerminal(root.Writer) || !isTerminal(os.Stderr) {
		return &adminSetupError{message: "Run setup admin in a terminal without pipes or redirection. Use help setup admin for noninteractive instructions.", code: 3}
	}
	ctx, stop := adminSetupSignalContext(ctx)
	defer stop()
	if _, err := fmt.Fprintf(os.Stderr, "Admin key verification\n\nNeed a key? Open:\n  https://platform.openai.com/settings/organization/admin-keys\nSelect your organization.\nCreate an admin key.\nCopy the key into the hidden prompt below.\nOnly organization owners can create admin keys.\nIf you are not an owner, ask an owner to run the operation without sharing their key.\n\nDestination: %s\nThe key stays in this process. Ctrl+C cancels.\n\n", destination); err != nil {
		return &adminSetupError{message: "Could not display admin setup. No verification request was sent."}
	}
	key, err := readAdminSetupKey(ctx, os.Stdin, os.Stderr)
	defer clear(key)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return adminSetupCanceled(ctx)
	}
	if errors.Is(err, errAdminSetupKeyEmpty) {
		return &adminSetupError{message: "No key was entered. Run setup admin again to retry."}
	}
	if err != nil {
		return &adminSetupError{message: "Could not read the key securely. Paste one key without spaces or line breaks, then press Enter."}
	}
	if len(key) == 0 {
		return &adminSetupError{message: "No key was entered. Run setup admin again to retry."}
	}
	if _, err := fmt.Fprintln(os.Stderr, "Verifying admin access..."); err != nil {
		return &adminSetupError{message: "Could not display admin setup. No verification request was sent."}
	}
	if err := verifyAdminSetup(ctx, options, key); err != nil {
		if ctx.Err() != nil {
			return adminSetupCanceled(ctx)
		}
		return err
	}
	if _, err := fmt.Fprintln(root.Writer, "Admin access verified. The project-list request succeeded.\nNo key was saved. Later commands still need authentication."); err != nil {
		return &adminSetupError{message: "Verification succeeded, but the CLI could not display the result. No key was saved."}
	}
	return nil
}

func adminSetupRequestOptions(command *cli.Command) ([]option.RequestOption, string, error) {
	baseURL := command.String("base-url")
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, "", &adminSetupError{message: "Admin setup needs a base URL without embedded credentials, a query, or a fragment.", code: 3}
	}
	ip := net.ParseIP(endpoint.Hostname())
	loopback := strings.EqualFold(endpoint.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
	if !strings.EqualFold(endpoint.Scheme, "https") && !(strings.EqualFold(endpoint.Scheme, "http") && loopback) {
		return nil, "", &adminSetupError{message: "Admin setup requires HTTPS, except for a local loopback server.", code: 3}
	}
	headers, err := requestHeaders(command)
	if err != nil {
		return nil, "", err
	}
	options := GetDefaultRequestOptions(command)
	for name, values := range headers {
		options = append(options, option.WithHeader(name, values[0]))
	}
	// Keep the configured transport, including mTLS, but never forward a newly
	// entered credential through a redirect. Do not change the shared client.
	client := *http.DefaultClient
	if configured, ok := command.Root().Metadata[mtlsHTTPClientMetadata].(*http.Client); ok && configured != nil {
		client = *configured
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	options = append(options, option.WithBaseURL(baseURL), option.WithHTTPClient(&client), option.WithMaxRetries(0))
	// Spell Unicode and terminal controls explicitly without changing the URL.
	origin := strconv.QuoteToASCII(endpoint.Scheme + "://" + endpoint.Host)
	return options, origin[1 : len(origin)-1], nil
}

func verifyAdminSetup(ctx context.Context, options []option.RequestOption, key []byte) error {
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := openai.NewClient(options...)
	var response *http.Response
	page, err := client.Admin.Organization.Projects.List(check, openai.AdminOrganizationProjectListParams{Limit: openai.Int(1)},
		option.WithAdminAPIKey(string(key)), option.WithHeader("Authorization", "Bearer "+string(key)), option.WithResponseInto(&response))
	if response != nil && response.StatusCode >= 300 && response.StatusCode < 400 {
		return &adminSetupError{message: "Verification stopped at a redirect. Check the configured base URL."}
	}
	if err == nil {
		if page != nil && page.JSON.Data.Valid() {
			return nil
		}
		return &adminSetupError{message: "The server did not return a project list. Check the configured base URL."}
	}
	// Discard backend errors. Their bodies and headers can reflect the key.
	if check.Err() != nil {
		if ctx.Err() != nil {
			return adminSetupCanceled(ctx)
		}
		return &adminSetupError{
			message: "Verification timed out after 15 seconds. Check your connection, then run setup admin again.",
			cause:   context.DeadlineExceeded,
		}
	}
	var apiError *openai.Error
	if errors.As(err, &apiError) {
		switch {
		case apiError.StatusCode == http.StatusUnauthorized:
			return &adminSetupError{message: "The server rejected the admin key (401). Create an admin key for your organization, then retry."}
		case apiError.StatusCode == http.StatusForbidden:
			return &adminSetupError{message: "The server denied admin access (403). Ask an organization owner to check your access."}
		case apiError.StatusCode >= 300 && apiError.StatusCode < 400:
			return &adminSetupError{message: "Verification stopped at a redirect. Check the configured base URL."}
		default:
			return &adminSetupError{message: "The server could not verify admin access. Try again later."}
		}
	}
	return &adminSetupError{message: "Could not verify admin access. Check your connection and the configured base URL."}
}

var errAdminSetupTerminated = errors.New("admin setup terminated")

func adminSetupSignalContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			if received == syscall.SIGTERM {
				cancel(errAdminSetupTerminated)
			} else {
				cancel(context.Canceled)
			}
		case <-ctx.Done():
		}
	}()
	return ctx, func() { signal.Stop(signals); cancel(nil); <-done }
}

func adminSetupCanceled(ctx context.Context) error {
	// Preserve the caller's standard cancellation identity without retaining an
	// arbitrary cancellation cause that could contain private request details.
	cause := ctx.Err()
	if cause == nil {
		cause = context.Canceled
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return &adminSetupError{message: "Admin setup timed out. No key was saved.", cause: cause}
	}
	code := 130
	if errors.Is(context.Cause(ctx), errAdminSetupTerminated) {
		code = 143
	}
	return &adminSetupError{message: "Admin setup canceled. No key was saved.", code: code, cause: cause}
}
