package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/openai/openai-cli/internal/audiohistory"
	localcommands "github.com/openai/openai-cli/internal/commands"
	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	filecommands "github.com/openai/openai-cli/internal/commands/files"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/tidwall/gjson"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func profileContextTestRoot(t *testing.T) *cli.Command {
	t.Helper()
	root := authStatusTestRoot(io.Discard)
	registerProjectLinkCommands(root)
	configureProfileContext(root)
	return root
}

func TestProfileContextWrappedFlagMatchesRequest(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, map[string]string{"OPENAI_CUSTOM_HEADERS": "OpenAI-Project: proj_header"})
	root := profileContextTestRoot(t)
	root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, command *cli.Command) error {
		report, err := resolveAuthStatus(root, os.LookupEnv)
		require.NoError(t, err)
		require.Equal(t, authStatusValue{Value: "proj_flag", Source: "flag:--project"}, report.Project)
		var got string
		options := append(GetDefaultRequestOptions(command), option.WithHTTPClient(profileContextTransport(func(request *http.Request) {
			got = request.Header.Get("OpenAI-Project")
		})))
		client := openai.NewClient(options...)
		_, err = client.Models.List(ctx)
		require.NoError(t, err)
		require.Equal(t, report.Project.Value, got)
		return nil
	}})
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project", "proj_flag", "probe"}))
}

type profileContextTransport func(*http.Request)

func (capture profileContextTransport) Do(request *http.Request) (*http.Response, error) {
	capture(request)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"data":[],"object":"list"}`)), Request: request}, nil
}

func TestProfileContextSnapshotAndCommandReuse(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, nil)
	t.Setenv("OPENAI_PROFILE", "work")
	path, err := profileStorePath()
	require.NoError(t, err)
	first, second := "proj_first", "proj_second"
	_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["work"] = profileValues{Project: &first}
		return nil
	})
	require.NoError(t, err)
	root := profileContextTestRoot(t)
	var got []string
	root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, command *cli.Command) error {
		for range 2 {
			options := append(GetDefaultRequestOptions(command), option.WithHTTPClient(profileContextTransport(func(request *http.Request) {
				got = append(got, request.Header.Get("OpenAI-Project"))
			})))
			client := openai.NewClient(options...)
			_, err := client.Models.List(ctx)
			if err != nil {
				return err
			}
			_, err = updateProfiles(ctx, path, func(registry *profileRegistry) error {
				registry.Profiles["work"] = profileValues{Project: &second}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	}})
	require.NoError(t, root.Run(t.Context(), []string{"openai", "probe"}))
	require.Equal(t, []string{first, first}, got)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project=", "probe"}))
	require.Equal(t, []string{first, first, "", ""}, got)
}

func TestProfileContextEnvironmentSnapshot(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, nil)
	root := profileContextTestRoot(t)
	calls := make(map[string]int)
	root.Command("auth").Command("status").Action = func(ctx context.Context, command *cli.Command) error {
		_, err := resolveAuthStatus(root, func(name string) (string, bool) {
			calls[name]++
			return "", false
		})
		return err
	}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "auth", "status"}))
	require.Len(t, calls, 7)
	for name, count := range calls {
		require.Equal(t, 1, count, name)
	}
}

func TestProfileContextSelectorCommandReuse(t *testing.T) {
	for _, useEnvironment := range []bool{false, true} {
		name := "saved default"
		if useEnvironment {
			name = "environment"
		}
		t.Run(name, func(t *testing.T) {
			projectLinkTestEnvironment(t)
			authStatusTestEnvironment(t, nil)
			if useEnvironment {
				t.Setenv("OPENAI_PROFILE", "default")
			}
			path, err := profileStorePath()
			require.NoError(t, err)
			work, fallback, selected := "proj_work", "proj_default", "default"
			_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				registry.Profiles["work"] = profileValues{Project: &work}
				registry.Profiles["default"] = profileValues{Project: &fallback}
				registry.DefaultProfile = &selected
				return nil
			})
			require.NoError(t, err)
			root := profileContextTestRoot(t)
			var got []string
			root.Command("auth").Command("status").Action = func(_ context.Context, _ *cli.Command) error {
				report, err := resolveAuthStatus(root, os.LookupEnv)
				got = append(got, report.Project.Value)
				return err
			}
			for _, args := range [][]string{
				{"openai", "--profile=work", "auth", "status"},
				{"openai", "auth", "status"},
				{"openai", "--profile=", "auth", "status"},
				{"openai", "auth", "status"},
			} {
				require.NoError(t, root.Run(t.Context(), args))
			}
			require.Equal(t, []string{work, fallback, "", fallback}, got)
		})
	}
}

func TestProfileContextStatusEscapesProfileSource(t *testing.T) {
	projectLinkTestEnvironment(t)
	name := "work\u202e\u2028demo"
	authStatusTestEnvironment(t, map[string]string{"OPENAI_PROFILE": name})
	path, err := profileStorePath()
	require.NoError(t, err)
	organization, project, endpoint := "org_demo", "proj_demo", "https://example.invalid/v1"
	_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles[name] = profileValues{Organization: &organization, Project: &project, BaseURL: &endpoint}
		return nil
	})
	require.NoError(t, err)
	for _, format := range []string{"text", "json"} {
		var output bytes.Buffer
		root := profileContextTestRoot(t)
		root.Writer = &output
		require.NoError(t, root.Run(t.Context(), []string{"openai", "auth", "status", "--format=" + format}))
		if format == "text" {
			require.NotContains(t, output.String(), "\u202e")
			require.NotContains(t, output.String(), "\u2028")
			require.Equal(t, 3, strings.Count(output.String(), `[profile:work\u202e\u2028demo]`))
		} else {
			var report authStatusReport
			require.NoError(t, json.Unmarshal(output.Bytes(), &report))
			for _, source := range []string{report.Organization.Source, report.Project.Source, report.BaseURL.Source} {
				require.Equal(t, "profile:"+name, source)
			}
		}
	}
}

func TestProfileContextBatchHint(t *testing.T) {
	for _, tc := range []struct {
		name, environment string
		args              []string
		needsOptions      bool
	}{
		{"environment profile", "profile_environment", nil, false},
		{"empty environment profile", "", nil, false},
		{"explicit profile", "profile_environment", []string{"--profile", "profile_override"}, true},
		{"matching profile", "profile_environment", []string{"--profile", "profile_environment"}, true},
		{"empty profile override", "profile_environment", []string{"--profile="}, true},
		{"empty profile without environment value", "", []string{"--profile="}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENAI_PROFILE", tc.environment)
			var output bytes.Buffer
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Commands: []*cli.Command{{Name: "batches", Commands: []*cli.Command{{
					Name: "create", Action: func(ctx context.Context, command *cli.Command) error {
						return batchcommands.ShowNextCommand(gjson.Parse(`{"id":"batch-synthetic"}`), batchcommands.HintOptions{
							Context: ctx, Route: "(resource) batches > (method) create", Response: true,
							Terminal: true, Format: "text", Stderr: &output,
							HumanTerminal: func(context.Context, *cli.Command) bool { return true },
						})
					},
				}}}},
			}
			configureProfileContext(root)
			batchcommands.Configure(root, batchcommands.Options{})
			args := append([]string{"openai"}, tc.args...)
			require.NoError(t, root.Run(t.Context(), append(args, "batches", "create")))
			if tc.needsOptions {
				require.Equal(t, "To wait for this batch, reuse the same connection and authentication options with batches retrieve --wait.\n", output.String())
			} else {
				require.Equal(t, "Follow this batch:\n  openai batches retrieve --batch-id=batch-synthetic --wait\n", output.String())
			}
			require.NotContains(t, output.String(), "profile_environment")
			require.NotContains(t, output.String(), "profile_override")
		})
	}
}

// Byte slices preserve malformed UTF-8 and NUL fixtures across the subprocess boundary.
type profileReceiptFixture struct {
	Arguments            [][]byte
	Environment          string
	Executable           string
	ChildProfile         bool
	BlockForCancellation bool
}

func profileReceiptArguments(args ...string) [][]byte {
	result := make([][]byte, len(args))
	for i, arg := range args {
		result[i] = []byte(arg)
	}
	return result
}

// The action replaces the API response, while public configuration captures the invocation.
func profileContextFileAndAudio(t *testing.T, fixture profileReceiptFixture) (*audioWorkbench, []string, bool, string) {
	t.Helper()
	authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "sk-fake-inherited"})
	t.Setenv("OPENAI_PROFILE", fixture.Environment)
	for _, name := range []string{"OPENAI_WEBHOOK_SECRET", requestconfig.MTLSClientCertFileEnv, requestconfig.MTLSClientKeyFileEnv} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
	var model *audioWorkbench
	var requestArgs []string
	var omitHint bool
	var receipt bytes.Buffer
	create := &cli.Command{Name: "create", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "file", Required: true, BodyPath: "file", FileInput: true},
		&requestflag.Flag[string]{Name: "purpose", Required: true, BodyPath: "purpose"},
	}, Action: func(ctx context.Context, command *cli.Command) error {
		requestArgs, omitHint = requestconfig.NextCommandOptions(command.Root())
		model = newAudioWorkbench(ctx, command, nil, audiohistory.Store{})
		handled, err := filecommands.ShowResult(gjson.Parse(`{"object":"file","id":"file-example","filename":"upload space.txt","purpose":"user_data","bytes":13}`), filecommands.ResultOptions{
			ReadableOptions: clioutput.ReadableOptions{
				Context: ctx, Route: transformers.Route{Operation: "(resource) files > (method) create", OutputKind: transformers.OutputResponse},
				FormatOptions: clioutput.FormatOptions{Format: "text"}, Terminal: true, Stdout: &receipt, Stderr: io.Discard,
			},
		})
		require.True(t, handled, "the public Files presenter must handle the upload")
		return err
	}}
	if fixture.ChildProfile {
		create.Flags = append(create.Flags, &cli.StringFlag{Name: "profile"})
	}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Metadata: map[string]any{"help-invocation": audioShellQuote(fixture.Executable)},
		Flags:    []cli.Flag{&cli.StringFlag{Name: "base-url"}, requestconfig.NewRequestHeaderFlag()},
		Commands: []*cli.Command{{Name: "files", Commands: []*cli.Command{create}}},
	}
	for _, pair := range [][2]string{{"api-key", "OPENAI_API_KEY"}, {"admin-api-key", "OPENAI_ADMIN_KEY"}, {"webhook-secret", "OPENAI_WEBHOOK_SECRET"}, {"organization", "OPENAI_ORG_ID"}, {"project", "OPENAI_PROJECT_ID"}} {
		root.Flags = append(root.Flags, &requestflag.Flag[string]{Name: pair[0], Sources: cli.EnvVars(pair[1])})
	}
	root.Flags = append(root.Flags, requestconfig.MTLSClientFlags()...)
	configureProfileContext(root)
	requestconfig.ConfigureRootFlags(root)
	localcommands.ConfigureTasks(root)
	filecommands.Configure(root)
	args := []string{fixture.Executable}
	for _, arg := range fixture.Arguments {
		args = append(args, string(arg))
	}
	require.NoError(t, root.Run(t.Context(), args))
	require.NotNil(t, model, "upload action must run")
	return model, requestArgs, omitHint, receipt.String()
}

// The shell owns the helper PID and records its wait before the command returns.
func profileContextFileReceiptCommand(t *testing.T, ctx context.Context, shell string, fixture profileReceiptFixture) (*exec.Cmd, string, string) {
	t.Helper()
	shellPath := "/bin/" + shell
	if _, err := os.Stat(shellPath); err != nil {
		if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ","+shell+",") {
			t.Fatalf("required native shell %s is unavailable", shell)
		}
		t.Skipf("native %s is unavailable", shell)
	}
	testBinary, err := os.Executable()
	require.NoError(t, err)
	directory := t.TempDir()
	fixturePath, receiptPath := filepath.Join(directory, "fixture.json"), filepath.Join(directory, "receipt.txt")
	encoded, err := json.Marshal(fixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fixturePath, encoded, 0600))
	reapedPath, childPath, cancelPath := filepath.Join(directory, "reaped"), filepath.Join(directory, "child"), filepath.Join(directory, "cancel")
	const script = `helper=
cleanup() {
  trap '' TERM INT
  : > "$4"
  helper=${helper:-$!}
  if [ -n "$helper" ]; then
    printf '%s\n' "$helper" > "$3"
    wait "$helper" 2>/dev/null
    [ "$?" -ne 127 ] || exit 131
    printf '%s\n' "$helper" > "$2"
  fi
  exit 130
}
trap 'cleanup "$@"' TERM INT
"$1" -test.run='^TestFileAndAudioCopiedCommandsPreserveProfileSelector$' -test.count=1 -test.timeout=5s &
helper=$!
printf '%s\n' "$helper" > "$3"
wait "$helper"
helper_status=$?
[ "$helper_status" -ne 127 ] || exit 131
printf '%s\n' "$helper" > "$2"
trap - TERM INT
exit "$helper_status"`
	args := []string{"--noprofile", "--norc", "-c", script, "profile-receipt-helper", testBinary, reapedPath, childPath, cancelPath}
	if shell == "zsh" {
		args = []string{"-f", "-c", script, "profile-receipt-helper", testBinary, reapedPath, childPath, cancelPath}
	}
	process := exec.CommandContext(ctx, shellPath, args...)
	process.WaitDelay = time.Second
	process.Cancel = func() error {
		// The fallback runs inside the owned helper. Never signal a stored helper PID.
		markerErr := os.WriteFile(cancelPath, nil, 0600)
		signalErr := process.Process.Signal(syscall.SIGTERM)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			child, childErr := os.ReadFile(childPath)
			reaped, reapErr := os.ReadFile(reapedPath)
			pid, pidErr := strconv.Atoi(strings.TrimSpace(string(child)))
			if childErr == nil && reapErr == nil && pidErr == nil && pid > 0 && bytes.Equal(child, reaped) {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		return fmt.Errorf("receipt shell did not confirm helper reaping: marker=%v signal=%v", markerErr, signalErr)
	}
	process.Dir = directory
	process.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "HOME=" + directory,
		"XDG_CONFIG_HOME=" + directory, "TMPDIR=" + directory, "GOMAXPROCS=2",
		"OPENAI_CLI_PROFILE_RECEIPT_FIXTURE=" + fixturePath, "OPENAI_CLI_PROFILE_RECEIPT_OUTPUT=" + receiptPath,
		"OPENAI_CLI_PROFILE_RECEIPT_CANCEL=" + cancelPath}
	return process, receiptPath, reapedPath
}

// A real parent shell selects Files' receipt quoting. Keep it alive until the helper exits.
func profileContextNativeFileReceipt(t *testing.T, shell string, fixture profileReceiptFixture) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process, receiptPath, reapedPath := profileContextFileReceiptCommand(t, ctx, shell, fixture)
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	require.NoError(t, process.Run(), "shell=%s stdout=%q stderr=%q", shell, stdout.String(), stderr.String())
	require.NoError(t, ctx.Err())
	require.Empty(t, stderr.String())
	reaped, err := os.ReadFile(reapedPath)
	require.NoError(t, err)
	require.NotEmpty(t, bytes.TrimSpace(reaped), "the shell must reap its helper")
	child, err := os.ReadFile(filepath.Join(filepath.Dir(reapedPath), "child"))
	require.NoError(t, err)
	require.Equal(t, string(child), string(reaped), "the shell must wait for its own helper")
	result, err := os.ReadFile(receiptPath)
	require.NoError(t, err)
	return string(result)
}

func TestProfileContextReceiptCancellation(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			process, readyPath, reapedPath := profileContextFileReceiptCommand(t, ctx, shell, profileReceiptFixture{BlockForCancellation: true})
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &stderr
			require.NoError(t, process.Start())
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = process.Wait(); close(done) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(4 * time.Second):
					t.Error("receipt shell did not stop after cancellation")
				}
			}()
			var ready []byte
			require.Eventually(t, func() bool {
				ready, _ = os.ReadFile(readyPath)
				return len(bytes.TrimSpace(ready)) != 0
			}, 2*time.Second, 10*time.Millisecond, "helper did not report readiness")
			cancel()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("receipt shell did not finish bounded cleanup")
			}
			require.Error(t, waitErr)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			reaped, err := os.ReadFile(reapedPath)
			require.NoError(t, err)
			require.Equal(t, string(ready), string(reaped), "the shell must wait for the ready helper")
			pid, err := strconv.Atoi(strings.TrimSpace(string(ready)))
			require.NoError(t, err)
			helper, err := os.FindProcess(pid)
			require.NoError(t, err)
			err = helper.Signal(syscall.Signal(0))
			require.True(t, errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH), "helper remains active or unreaped: %v", err)
		})
	}
}

func TestFileReceiptRequestContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  []string
		omit  bool
	}{
		{"environment credentials", nil, nil, false},
		{"explicit profile", []string{"--profile=selected"}, []string{"--profile=selected"}, false},
		{"explicit empty profile", []string{"--profile="}, []string{"--profile="}, false},
		{"profile and request selection", []string{"--profile=selected", "--project=project-one"}, []string{"--profile=selected", "--project=project-one"}, false},
		{"request selection", []string{"--project=project-one", "--organization=org-one", "--base-url=https://example.invalid/proxy/v1"}, []string{"--project=project-one", "--organization=org-one", "--base-url=https://example.invalid/proxy/v1"}, false},
		{"explicit empty project", []string{"--project="}, []string{"--project="}, false},
		{"same inherited key", []string{"--api-key=sk-fake-inherited"}, nil, false},
		{"different key", []string{"--api-key=sk-fake-override"}, nil, true},
		{"empty key override", []string{"--api-key="}, nil, true},
		{"admin key override", []string{"--admin-api-key=sk-fake-admin"}, nil, true},
		{"webhook secret override", []string{"--webhook-secret=fake-secret"}, nil, true},
		{"header override", []string{"-H", "Authorization: Bearer fake-secret"}, nil, true},
		{"certificate override", []string{"--mtls-client-cert-file=/synthetic/private/cert.pem"}, nil, true},
		{"private key override", []string{"--mtls-client-key-file=/synthetic/private/key.pem"}, nil, true},
		{"URL user info", []string{"--base-url=https://fake:secret@example.invalid"}, nil, true},
		{"URL query", []string{"--base-url=https://example.invalid/v1?key=fake-secret"}, nil, true},
		{"URL fragment", []string{"--base-url=https://example.invalid/v1#fake-secret"}, nil, true},
		{"URL empty query", []string{"--base-url=https://example.invalid/v1?"}, nil, true},
		{"invalid project", []string{"--project=proj\x00name"}, nil, true},
		{"invalid profile NUL", []string{"--profile=bad\x00name"}, nil, true},
		{"invalid profile UTF-8", []string{"--profile=bad\xffname"}, nil, true},
		{"profile with secret override", []string{"--profile=selected", "--api-key=sk-fake-override"}, nil, true},
		{"profile with private URL", []string{"--profile=selected", "--base-url=https://example.invalid/v1?key=fake-secret"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"files", "upload", "source.txt", "--purpose=user_data"}, tc.flags...)
			fixture := profileReceiptFixture{Arguments: profileReceiptArguments(args...), Environment: "inherited-profile", Executable: "openai"}
			model, requestArgs, omitHint, receipt := profileContextFileAndAudio(t, fixture)
			require.Equal(t, tc.want, requestArgs)
			require.Equal(t, tc.omit, omitHint)
			require.Contains(t, receipt, "Uploaded upload space.txt (13 B)")
			if tc.omit {
				require.NotContains(t, receipt, "Inspect it:")
			}
			for _, secret := range []string{"sk-fake", "fake-secret", "/synthetic/private/"} {
				require.NotContains(t, receipt, secret)
			}
			model.input.SetValue("SYNTHETIC_PRIVATE_EDITOR_TEXT")
			preview := model.commandPreview()
			if tc.omit {
				require.Equal(t, "Command preview unavailable with private request overrides.", preview)
			} else {
				require.Contains(t, preview, "OPENAI_UNTRUSTED_STDIN=true")
			}
			for _, secret := range []string{"sk-fake", "fake-secret", "/synthetic/private/", "inherited-profile"} {
				require.NotContains(t, preview, secret)
			}
			t.Run("native receipt", func(t *testing.T) {
				receipt := profileContextNativeFileReceipt(t, "bash", fixture)
				require.Equal(t, !tc.omit, strings.Contains(receipt, "Inspect it:"))
				for _, secret := range []string{"sk-fake", "fake-secret", "/synthetic/private/", "inherited-profile"} {
					require.NotContains(t, receipt, secret)
				}
			})
		})
	}
}

func TestFileAndAudioCopiedCommandsPreserveProfileSelector(t *testing.T) {
	if path := os.Getenv("OPENAI_CLI_PROFILE_RECEIPT_FIXTURE"); path != "" {
		// Cooperative fallback also stops a blocked helper if its parent trap is delayed.
		done := make(chan struct{})
		defer close(done)
		go func() {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				if _, err := os.Stat(os.Getenv("OPENAI_CLI_PROFILE_RECEIPT_CANCEL")); err == nil {
					os.Exit(130)
				}
				select {
				case <-done:
					return
				case <-ticker.C:
				}
			}
		}()
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var fixture profileReceiptFixture
		require.NoError(t, json.Unmarshal(data, &fixture))
		if fixture.BlockForCancellation {
			ready := strconv.Itoa(os.Getpid()) + "\n"
			require.NoError(t, os.WriteFile(os.Getenv("OPENAI_CLI_PROFILE_RECEIPT_OUTPUT"), []byte(ready), 0600))
			time.Sleep(time.Minute)
			t.Fatal("blocked receipt helper survived cancellation")
		}
		// Configure captures os.Args[0], as it does for the actual CLI process.
		previous := os.Args
		os.Args = []string{fixture.Executable}
		defer func() { os.Args = previous }()
		_, _, _, receipt := profileContextFileAndAudio(t, fixture)
		require.NoError(t, os.WriteFile(os.Getenv("OPENAI_CLI_PROFILE_RECEIPT_OUTPUT"), []byte(receipt), 0600))
		return
	}
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, directory := audioPreviewExecutable(t, "openai preview's binary")
			for _, tc := range []struct {
				name, environment, rootFlag, childFlag string
				want                                   []string
			}{
				{"explicit", "inherited-profile", "--profile=team's \"$(printf altered)\" 世界\u202e", "", []string{"--profile=team's \"$(printf altered)\" 世界\u202e"}},
				{"explicit empty", "inherited-profile", "--profile=", "", []string{"--profile="}},
				{"inherited", "inherited-profile", "", "", nil},
				{"inherited empty", "", "", "", nil},
				{"child ownership", "inherited-profile", "", "--profile=/synthetic/private/child", nil},
				{"root and child ownership", "inherited-profile", "--profile=selected", "--profile=/synthetic/private/child", []string{"--profile=selected"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var args []string
					if tc.rootFlag != "" {
						args = append(args, tc.rootFlag)
					}
					args = append(args, "files", "upload", "source.txt", "--purpose=user_data")
					if tc.childFlag != "" {
						args = append(args, tc.childFlag)
					}
					fixture := profileReceiptFixture{Arguments: profileReceiptArguments(args...), Environment: tc.environment,
						Executable: binary, ChildProfile: tc.childFlag != ""}
					model, requestArgs, omitHint, _ := profileContextFileAndAudio(t, fixture)
					require.Equal(t, tc.want, requestArgs)
					require.False(t, omitHint)
					receipt := profileContextNativeFileReceipt(t, shell, fixture)
					_, copiedFile, found := strings.Cut(receipt, "\nInspect it: ")
					require.True(t, found, receipt)
					fileCapture := executeAudioPreview(t, shell, strings.TrimSpace(copiedFile), directory)
					require.Equal(t, append(append([]string{}, tc.want...), "files", "get", "file-example"), fileCapture.args)
					require.Empty(t, fileCapture.stdin)
					require.Equal(t, "unset", fileCapture.protected)

					model.settings.Format = "wav"
					model.input.SetValue("@synthetic 'literal' text")
					preview := model.commandPreview()
					for _, copied := range []string{copiedFile, preview} {
						require.NotContains(t, copied, "/synthetic/private/")
						require.NotContains(t, copied, "inherited-profile")
						for _, character := range strings.TrimSpace(copied) {
							require.False(t, unicode.IsControl(character) || unicode.Is(unicode.Cf, character), "literal control in copied command")
						}
					}
					audioCapture := executeAudioPreview(t, shell, preview, directory)
					require.Equal(t, append(append([]string{}, tc.want...), "audio", "speak", "--output", "speech.wav"), audioCapture.args)
					require.Equal(t, "true", audioCapture.protected)
					assertAudioPreviewJSON(t, model, audioCapture.stdin)
				})
			}
		})
	}
}
