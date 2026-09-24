package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestLocalErrorMessageDoesNotExposeDiagnostics(t *testing.T) {
	const secret = "fake-sensitive-marker"
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&cli.StringFlag{Name: "model"}, &cli.IntFlag{Name: "limit"},
		&cli.StringFlag{Name: "header", Aliases: []string{"H"}},
	}}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"unknown", errors.New(secret), "The command could not be completed."},
		{"wrapped unknown", fmt.Errorf("https://%s@example.invalid: %w", secret, errors.New(secret)), "The command could not be completed."},
		{"canceled", fmt.Errorf("%s: %w", secret, context.Canceled), "Request canceled."},
		{"deadline", fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), "The request timed out."},
		{"timeout", &net.DNSError{Name: secret, Err: secret, IsTimeout: true}, "The request timed out."},
		{"URL", &url.Error{Op: "Get", URL: "https://" + secret + "@example.invalid/?token=" + secret, Err: errors.New(secret)}, "Could not connect to the API."},
		{"network", &net.OpError{Op: secret, Net: secret, Err: errors.New(secret)}, "Could not connect to the API."},
		{"JSON type", &json.UnmarshalTypeError{Value: secret, Field: secret}, "Could not decode JSON: a value has an unexpected type."},
		{"missing file", &os.PathError{Op: "open", Path: secret, Err: os.ErrNotExist}, "A local file could not be found."},
		{"file permission", &os.PathError{Op: "open", Path: secret, Err: os.ErrPermission}, "A local file could not be accessed."},
		{"other file error", &os.PathError{Op: secret, Path: secret, Err: errors.New(secret)}, "A local file could not be read or written."},
		{"base URL env", fmt.Errorf("OPENAI_BASE_URL %q is missing a scheme (expected http:// or https://)", secret), "OPENAI_BASE_URL must start with http:// or https://."},
		{"base URL flag", fmt.Errorf("--base-url %q is missing a scheme (expected http:// or https://)", secret), "--base-url must start with http:// or https://."},
		{"stdin env", fmt.Errorf("invalid OPENAI_UNTRUSTED_STDIN value %q: expected a boolean", secret), "OPENAI_UNTRUSTED_STDIN: expected a boolean"},
		{"invalid value", fmt.Errorf("invalid value %q for flag -limit: %s", secret, secret), "Invalid value for --limit."},
		{"quoted misleading flag", fmt.Errorf("invalid value %q for flag -limit: %s", `\" for flag -model: `+secret, secret), "Invalid value for --limit."},
		{"invalid alias", fmt.Errorf("invalid value %q for flag -H: %s", secret, secret), "Invalid value for --header."},
		{"unknown invalid flag", fmt.Errorf("invalid value %q for flag -%s: invalid", secret, secret), "The command could not be completed."},
		{"unknown flag", errors.New("flag provided but not defined: -" + secret), "An option is not recognized."},
		{"unknown help topic", fmt.Errorf("Unknown help topic %q. Run %s help --all to see commands.", secret, secret), "Unknown help topic. Run openai help --all to see commands."},
		{"invalid YAML", errors.New("Failed to parse piped data as YAML/JSON:\n" + secret), "Could not parse piped input as YAML or JSON."},
		{"scalar request body", errors.New("Cannot merge flags with a body that is not a map: " + secret), "The request body must be a JSON or YAML object."},
		{"form body", errors.New("Cannot send a non-map value to a form-encoded endpoint: " + secret), "The request body must be a JSON or YAML object."},
		{"binary body", errors.New("Unsupported body for application/octet-stream: " + secret), "The request body is not supported for this binary endpoint."},
		{"mTLS with appended secret", errors.New("mTLS requires an explicit HTTPS base URL " + secret), "The command could not be completed."},
		{"header with appended secret", errors.New("header 1: invalid character in name " + secret), "The command could not be completed."},
		{"required unknown flag", fmt.Errorf("Required flag %q not set", secret), "The command could not be completed."},
		{"missing unknown flag value", errors.New("flag needs an argument: --" + secret), "The command could not be completed."},
		{"nil", nil, "The command could not be completed."},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := localErrorMessage(root, test.err)
			if !strings.Contains(got, test.want) {
				t.Errorf("message = %q, want %q", got, test.want)
			}
			if strings.Contains(got, secret) || strings.ContainsAny(got, "\x1b\r") {
				t.Errorf("message included an untrusted diagnostic: %q", got)
			}
		})
	}
	var data any
	err := json.Unmarshal([]byte(`{"fake-sensitive-marker":`), &data)
	if got := localErrorMessage(root, err); got != "Could not decode JSON: invalid JSON syntax." {
		t.Errorf("JSON syntax message = %q", got)
	}
}

func TestLocalErrorMessageUntrustedStdinGuidance(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&cli.StringFlag{Name: "file", Aliases: []string{"f"}},
		&cli.StringFlag{Name: "upload"},
	}}
	for _, test := range []struct{ name, path, flag, suffix, want string }{
		{"file", "fake-sensitive-path", "file", "", "provide --file explicitly"},
		{"upload", "fake-sensitive-path", "upload", "", "provide --upload explicitly"},
		{"alias", "fake-sensitive-path", "f", "", "provide --file explicitly"},
		{"quoted path", `fake-sensitive-path" from piped YAML/JSON is disabled when OPENAI_UNTRUSTED_STDIN is enabled; provide --upload explicitly`, "file", "", "provide --file explicitly"},
		{"unknown flag", "fake-sensitive-path", "fake-sensitive-flag", "", "The command could not be completed."},
		{"appended data", "fake-sensitive-path", "file", " fake-sensitive-value", "The command could not be completed."},
		{"control in flag", "fake-sensitive-path", "file\x1b[2J", "", "The command could not be completed."},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := fmt.Errorf("file input %q from piped YAML/JSON is disabled when OPENAI_UNTRUSTED_STDIN is enabled; provide --%s explicitly%s", test.path, test.flag, test.suffix)
			got := localErrorMessage(root, &commandError{command: root, err: err})
			if !strings.Contains(got, test.want) || strings.Contains(got, "fake-sensitive-") || strings.ContainsRune(got, '\x1b') {
				t.Errorf("message = %q, want %q without private data", got, test.want)
			}
		})
	}
	got := localErrorMessage(root, fmt.Errorf("invalid OPENAI_UNTRUSTED_STDIN value %q: expected a boolean", "fake-sensitive-value\x1b[2J"))
	if got != "OPENAI_UNTRUSTED_STDIN: expected a boolean (true or false)." {
		t.Errorf("invalid stdin security mode message = %q", got)
	}
}

func TestLocalErrorMessagePreservesSafeGuidance(t *testing.T) {
	for _, message := range []string{
		"mTLS client certificate and key files must be configured together",
		"mTLS requires an explicit HTTPS base URL",
		"mTLS client certificate file does not exist",
		"mTLS client key file is not readable",
		"mTLS does not support HTTPS proxies",
		"cannot follow HTTP 307 redirect: streamed multipart uploads are not replayable",
		"cannot follow HTTP 308 redirect: streamed multipart uploads are not replayable",
		"header 1: expected 'Name: Value'",
		"header 1: name must not be empty",
		"header 2: invalid character in name",
		"header 3: invalid control character in value",
		"Setup help takes no additional arguments.",
	} {
		t.Run(message, func(t *testing.T) {
			if got := localErrorMessage(nil, fmt.Errorf("fake-sensitive-wrapper: %w", errors.New(message))); got != message {
				t.Errorf("message = %q, want %q", got, message)
			}
		})
	}
}

func TestLocalErrorMessageUsesDeclaredFlags(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--limit", "fake-sensitive-value"}, "Invalid value for --limit."},
		{[]string{"--model"}, "Add a value for --model."},
		{nil, "Missing required options: --model."},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			var failedCommand *cli.Command
			leaf := &cli.Command{
				Name:  "retrieve",
				Flags: []cli.Flag{&cli.StringFlag{Name: "model", Required: true}, &cli.IntFlag{Name: "limit"}},
				OnUsageError: func(_ context.Context, command *cli.Command, err error, _ bool) error {
					failedCommand = command
					return err
				},
			}
			root := &cli.Command{
				Name: "openai", Commands: []*cli.Command{leaf}, Writer: io.Discard, ErrWriter: io.Discard,
				Flags:          []cli.Flag{&cli.StringFlag{Name: "base-url"}},
				ExitErrHandler: func(context.Context, *cli.Command, error) {},
			}
			err := root.Run(t.Context(), append([]string{"openai", "retrieve"}, test.args...))
			if err == nil || failedCommand != leaf {
				t.Fatalf("expected leaf usage error, got %v", err)
			}
			err = &commandError{command: leaf, err: err}
			got := localErrorMessage(root, err)
			if !strings.Contains(got, test.want) || strings.Contains(got, "fake-sensitive") {
				t.Errorf("message = %q, want %q without rejected value", got, test.want)
			}
			for _, test := range []struct{ message, want string }{
				{`Required flags "model, limit" not set` + "\nRun 'fake-sensitive-help' for usage information", "Missing required options: --model, --limit."},
				{`invalid value "fake-sensitive-url" for flag -base-url: invalid`, "Invalid value for --base-url."},
			} {
				got := localErrorMessage(root, &commandError{command: leaf, err: errors.New(test.message)})
				if !strings.Contains(got, test.want) || strings.Contains(got, "fake-sensitive") {
					t.Errorf("message = %q, want %q", got, test.want)
				}
			}
		})
	}
}

func TestLocalErrorMessagePreservesProxyGuidance(t *testing.T) {
	const want = "mTLS does not support HTTPS proxies"
	proxyCalls := 0
	transport := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		proxyCalls++
		return nil, errors.New(want)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://fake-sensitive-user:fake-sensitive-password@synthetic.invalid/fake-sensitive-path?token=fake-sensitive-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The proxy callback fails before dialing, so this makes no network request.
	response, err := client.Do(request)
	var transportError *url.Error
	if response != nil || proxyCalls != 1 || !errors.As(err, &transportError) {
		t.Fatalf("expected one proxy failure wrapped by net/http; response=%v calls=%d error=%v", response, proxyCalls, err)
	}
	if got := localErrorMessage(nil, err); got != want || strings.Contains(got, "fake-sensitive-") {
		t.Errorf("message = %q, want %q without URL details", got, want)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		joined := errors.Join(err, cause)
		got := localErrorMessage(nil, joined)
		if cause == context.Canceled && got != "Request canceled." ||
			cause == context.DeadlineExceeded && !strings.HasPrefix(got, "The request timed out.") {
			t.Errorf("context error should take precedence over proxy guidance: %q", got)
		}
	}
}
