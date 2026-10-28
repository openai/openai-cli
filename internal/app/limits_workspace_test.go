package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const limitsControllerAlert = `{"object":"organization.spend_alert","id":"alert_synthetic","threshold_amount":10000,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["synthetic@example.invalid"]}}`
const limitsControllerRate = `{"object":"project.rate_limit","id":"rl_synthetic","model":"synthetic-model","max_requests_per_1_minute":100,"max_tokens_per_1_minute":200,"max_images_per_1_minute":0}`

type limitsControllerBackend struct {
	snapshot             limitsSnapshot
	loadErr, mutationErr error
	release              <-chan struct{}
	changes              chan limitsChange
	writes, reads        atomic.Int32
}

func (b *limitsControllerBackend) Load(ctx context.Context) (limitsSnapshot, error) {
	b.reads.Add(1)
	if err := ctx.Err(); err != nil {
		return limitsSnapshot{}, err
	}
	return b.snapshot, b.loadErr
}

func (b *limitsControllerBackend) Mutate(ctx context.Context, change limitsChange) (gjson.Result, error) {
	b.writes.Add(1)
	b.changes <- change
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return gjson.Result{}, ctx.Err()
		}
	}
	return gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10001,"currency":"USD","interval":"month"}`), b.mutationErr
}

func newLimitsController(t *testing.T, width, height int) (*limitsWorkspace, *limitsControllerBackend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	b := &limitsControllerBackend{snapshot: limitsSnapshot{LimitAbsent: true, AlertsComplete: true}, changes: make(chan limitsChange, 8)}
	m := &limitsWorkspace{ctx: ctx, cancel: cancel, backend: b, snapshot: b.snapshot, width: width, height: height, page: "home", shell: "bash",
		viewport: viewport.New(viewport.WithWidth(width-2), viewport.WithHeight(height-5))}
	m.viewport.SoftWrap, m.viewport.FillHeight = false, false
	t.Cleanup(func() { cancel(); m.work.Wait() })
	return m, b
}

func limitsControllerKey(m *limitsWorkspace, code rune) tea.Cmd {
	_, command := m.Update(tea.KeyPressMsg{Code: code})
	return command
}

func limitsControllerReview(t *testing.T, m *limitsWorkspace) {
	t.Helper()
	limitsControllerKey(m, tea.KeyEnter)
	if m.page != "form" || len(m.draft.inputs) != 1 {
		t.Fatalf("limit form did not open: page=%s", m.page)
	}
	m.draft.inputs[0].SetValue("100.01")
	limitsControllerKey(m, tea.KeyTab)
	limitsControllerKey(m, tea.KeyEnter)
	if m.page != "review" || m.apply || m.busy {
		t.Fatalf("review did not start on Back: page=%s apply=%t busy=%t", m.page, m.apply, m.busy)
	}
}

func limitsControllerRun(t *testing.T, command tea.Cmd) limitsWorkResult {
	t.Helper()
	if command == nil {
		t.Fatal("missing asynchronous operation")
	}
	result, ok := command().(limitsWorkResult)
	if !ok {
		t.Fatal("operation returned an unexpected message")
	}
	return result
}

func limitsControllerStarted(t *testing.T, m *limitsWorkspace, b *limitsControllerBackend) limitsChange {
	t.Helper()
	select {
	case change := <-b.changes:
		return change
	case <-m.ctx.Done():
		t.Fatal("confirmed mutation did not reach the backend")
		return limitsChange{}
	}
}

func TestLimitsWorkspaceReviewRequiresExplicitApply(t *testing.T) {
	m, b := newLimitsController(t, 80, 24)
	limitsControllerReview(t, m)
	m.work.Wait()
	if b.writes.Load() != 0 {
		t.Fatal("opening or reviewing a draft wrote to the backend")
	}
	limitsControllerKey(m, tea.KeyEnter)
	if m.page != "form" || b.writes.Load() != 0 {
		t.Fatal("default Back action submitted the draft")
	}
	m.setFocus(len(m.draft.inputs))
	limitsControllerKey(m, tea.KeyEnter)
	m.viewport.GotoTop()
	if m.viewport.AtBottom() {
		t.Fatal("fixture must require scrolling through its review")
	}
	limitsControllerKey(m, tea.KeyTab)
	if m.apply {
		t.Fatal("Apply became active before the review reached its end")
	}
	m.viewport.GotoBottom()
	limitsControllerKey(m, tea.KeyTab)
	if !m.apply || b.writes.Load() != 0 {
		t.Fatal("selecting Apply must not submit")
	}
	result := limitsControllerRun(t, limitsControllerKey(m, tea.KeyEnter))
	change := limitsControllerStarted(t, m, b)
	if b.writes.Load() != 1 || change.Kind != "limit_set" || change.Amount != 10001 || !change.BeforeMissing {
		t.Fatalf("incorrect confirmed mutation: %+v; writes=%d", change, b.writes.Load())
	}
	_, refresh := m.Update(result)
	m.Update(limitsControllerRun(t, refresh))
	if m.page != "home" || m.busy || b.reads.Load() != 1 || b.writes.Load() != 1 {
		t.Fatal("successful save did not refresh once without repeating the mutation")
	}
}

func TestLimitsWorkspaceResizeClearsApplyAndBlocksTinySubmission(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}, {39, 12}, {40, 11}} {
		m, b := newLimitsController(t, 80, 24)
		limitsControllerReview(t, m)
		m.viewport.GotoBottom()
		limitsControllerKey(m, tea.KeyTab)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if m.apply {
			t.Errorf("resize to %v retained Apply selection", size)
		}
		if m.small() {
			if view := m.View().Content; !strings.Contains(view, "Resize") || !strings.Contains(view, "Ctrl+C") {
				t.Errorf("tiny screen lacks resize/cancel guidance: %q", view)
			}
			m.apply = true // A hidden action must remain blocked even with stale focus.
			if command := limitsControllerKey(m, tea.KeyEnter); command != nil || m.busy {
				t.Errorf("tiny screen %v submitted an invisible action", size)
			}
		} else {
			limitsControllerKey(m, tea.KeyEnter)
			if m.page != "form" {
				t.Errorf("resize to %v did not reset confirmation to Back", size)
			}
		}
		m.work.Wait()
		if b.writes.Load() != 0 {
			t.Errorf("resize to %v caused a write", size)
		}
	}
}

func TestLimitsWorkspaceBusyAndFailedSavePreventRepeatedWrites(t *testing.T) {
	m, b := newLimitsController(t, 80, 24)
	release := make(chan struct{})
	b.release, b.mutationErr = release, errors.New("synthetic uncertain write failure")
	limitsControllerReview(t, m)
	m.viewport.GotoBottom()
	limitsControllerKey(m, tea.KeyTab)
	command := limitsControllerKey(m, tea.KeyEnter)
	limitsControllerStarted(t, m, b)
	for _, key := range []rune{tea.KeyEnter, tea.KeyTab, tea.KeyEnter, 'r', tea.KeyEscape} {
		if next := limitsControllerKey(m, key); next != nil {
			t.Error("busy workspace started another operation")
		}
	}
	if b.writes.Load() != 1 || !m.busy {
		t.Fatal("pending write lost its submission guard")
	}
	close(release)
	m.Update(limitsControllerRun(t, command))
	if !m.locked || m.apply || m.page != "form" || m.draft.inputs[0].Value() != "100.01" || !strings.Contains(m.formError, "Press ? for recovery") {
		t.Fatalf("failed save discarded draft or unlocked writes: page=%s locked=%t error=%q", m.page, m.locked, m.formError)
	}
	m.setFocus(len(m.draft.inputs))
	limitsControllerKey(m, tea.KeyEnter)
	limitsControllerKey(m, tea.KeyEscape)
	limitsControllerKey(m, tea.KeyEnter)
	m.work.Wait()
	if b.writes.Load() != 1 || m.busy || !m.locked {
		t.Fatal("failed save permitted an unconfirmed repeat")
	}
	limitsControllerKey(m, '?')
	if m.page != "details" || !strings.Contains(m.details, "It may have succeeded") || !strings.Contains(m.details, "help admin organization spend-limit retrieve") {
		t.Fatal("failed save did not provide uncertainty guidance and a safe read")
	}
}

func limitsControllerPrintedCommand(t *testing.T, m *limitsWorkspace) string {
	t.Helper()
	m.viewport.GotoBottom()
	view := ansi.Strip(m.View().Content)
	for _, visible := range []string{"Ctrl+P: print command and exit", "> Back", "Apply change"} {
		if !strings.Contains(view, visible) {
			t.Fatalf("review clipped %q at %d columns: %q", visible, m.width, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("review exceeds %d columns: %q", m.width, line)
		}
	}
	_, quit := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if quit == nil || !m.quitting || m.interrupted || m.commandToPrint == "" {
		t.Fatal("Ctrl+P did not freeze the reviewed command and quit normally")
	}
	var out bytes.Buffer
	if err := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: "text", Stdout: &out, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if out.String() != m.commandToPrint+"\n" {
		t.Fatalf("finalization changed the command or added prose: %q", out.String())
	}
	return out.String()
}

func TestLimitsWorkspacePrintFreezesReviewedCommand(t *testing.T) {
	m, backend := newLimitsController(t, 40, 12)
	limitsControllerReview(t, m)
	if !strings.Contains(m.details, "Command preview (inspection only)") {
		t.Fatal("wrapped review did not identify the command as inspection-only")
	}
	_, quit := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if quit == nil || m.commandToPrint == "" {
		t.Fatal("review did not offer command printing")
	}
	frozen := m.commandToPrint
	m.change.Amount, m.previewCommand = 99999, "changed after exit"
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var out bytes.Buffer
	err := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Stdout: &out, Stderr: io.Discard})
	if err != nil || out.String() != frozen+"\n" || backend.writes.Load() != 0 {
		t.Fatalf("print changed reviewed bytes or sent a mutation: output=%q err=%v", out.String(), err)
	}
}

func TestLimitsWorkspacePrintOmissionsAndUnavailableStates(t *testing.T) {
	for _, scenario := range []string{"private context", "unsupported shell", "literal prefix", "go run", "locked", "refresh required", "busy", "small terminal", "canceled", "output failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, backend := newLimitsController(t, 40, 12)
			m.change, m.page = limitsChange{Kind: "limit_set", Amount: 12525}, "review"
			omitted := true
			switch scenario {
			case "private context":
				m.omitPreview = true
			case "unsupported shell":
				m.shell = "unknown-shell"
			case "literal prefix":
				prefix := "\\@literal"
				m.change = limitsChange{Kind: "alert_create", SubjectPrefixSet: true, SubjectPrefix: &prefix}
			case "go run":
				m.invocation, m.omitPreview = "go run ./cmd/openai", true
			default:
				omitted = false
			}
			m.setReview()
			switch scenario {
			case "locked":
				m.locked = true
			case "refresh required":
				m.refreshRequired = true
			case "busy":
				m.busy = true
			case "small terminal":
				m.Update(tea.WindowSizeMsg{Width: 39, Height: 12})
			case "canceled":
				m.cancel()
			case "output failure":
				m.output = &limitsWorkspaceOutput{err: errors.New("synthetic sink failure")}
			}
			_, quit := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			if quit != nil || m.commandToPrint != "" || m.quitting || backend.writes.Load() != 0 {
				t.Fatal("Ctrl+P exported an omitted command or bypassed an unavailable state")
			}
			if omitted && (m.previewCommand != "" || strings.Contains(m.View().Content, "Ctrl+P:")) {
				t.Fatal("an omitted command retained its print action")
			}
		})
	}
}

func TestLimitsWorkspaceNULArgumentsDisablePrinting(t *testing.T) {
	ordinary, ordinaryBackend := newLimitsController(t, 40, 12)
	require.NotNil(t, limitsControllerKey(ordinary, 'q'))
	var ordinaryOutput bytes.Buffer
	require.NoError(t, finishLimitsWorkspace(t.Context(), ordinary, nil, ShowJSONOpts{Context: t.Context(), Stdout: &ordinaryOutput, Stderr: io.Discard}))
	require.Empty(t, ordinary.commandToPrint)
	require.Zero(t, ordinaryBackend.writes.Load())
	require.Zero(t, ordinaryBackend.reads.Load())

	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, source := range []string{"alert_id", "rate_id", "project_id", "request_option", "executable"} {
			t.Run(shell+"/"+source, func(t *testing.T) {
				m, backend := newLimitsController(t, 40, 12)
				m.shell = shell
				kind, record, edit := "alert_update", limitsControllerAlert, "100.01"
				switch source {
				case "alert_id":
					record = strings.Replace(record, "alert_synthetic", `alert_safe\u0000suffix`, 1)
				case "rate_id":
					kind, edit = "rate_update", "101"
					record = strings.Replace(limitsControllerRate, "rl_synthetic", `rate_safe\u0000suffix`, 1)
					m.snapshot.ProjectID = "proj_synthetic"
				case "project_id":
					m.snapshot.ProjectID = "proj_safe\x00suffix"
					record = strings.Replace(record, "organization.spend_alert", "project.spend_alert", 1)
				case "request_option":
					m.requestArgs = []string{"--organization=org_safe\x00suffix"}
				case "executable":
					m.previewExecutable = "/synthetic/openai\x00suffix"
				}
				m.begin(kind, gjson.Parse(record))
				require.Equal(t, "form", m.page)
				m.draft.inputs[0].SetValue(edit)
				m.review()
				require.Equal(t, "review", m.page, m.formError)
				require.Empty(t, m.previewCommand)
				inspection := limitsReview(m.change, m.snapshot.ProjectID, m.requestArgs, m.omitPreview, m.shell, m.previewExecutable)
				require.Contains(t, inspection, "Command preview omitted: the shell cannot preserve a NUL byte")
				require.NotContains(t, inspection, "Command preview (inspection only)")
				require.NotContains(t, inspection, "\x00")
				_, quit := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				require.Nil(t, quit)
				require.False(t, m.quitting)
				require.Empty(t, m.commandToPrint)
				limitsControllerKey(m, tea.KeyEscape)
				require.Equal(t, "form", m.page)
				require.Equal(t, edit, m.draft.inputs[0].Value())
				require.Equal(t, gjson.Get(record, "id").Str, m.draft.change.ID)
				limitsControllerKey(m, tea.KeyEscape)
				require.NotNil(t, limitsControllerKey(m, 'q'))
				var out bytes.Buffer
				require.NoError(t, finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Stdout: &out, Stderr: io.Discard}))
				require.Equal(t, ordinaryOutput.String(), out.String())
				require.Zero(t, backend.writes.Load())
				require.Zero(t, backend.reads.Load())
			})
		}
	}
}

func TestLimitsWorkspacePrintPreservesFinalizationFailures(t *testing.T) {
	for _, scenario := range []string{"caller cancellation", "interrupt", "program failure", "mutation failure", "terminal failure", "print failure", "short print"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := newLimitsController(t, 40, 12)
			limitsControllerReview(t, m)
			m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			ctx := t.Context()
			want := errors.New("synthetic independent failure")
			var out bytes.Buffer
			opts := ShowJSONOpts{Context: ctx, Format: "text", Stdout: &out, Stderr: io.Discard}
			var runErr error
			switch scenario {
			case "caller cancellation":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "interrupt":
				m.interrupted = true
				want = context.Canceled
			case "program failure":
				runErr = want
			case "mutation failure":
				m.locked, m.mutationError = true, want
			case "terminal failure":
				m.output = &limitsWorkspaceOutput{err: want}
			case "print failure":
				opts.Stdout = limitsFailingWriter{err: want}
			case "short print":
				want = io.ErrShortWrite
				opts.Stdout = limitsWorkspaceWriteFunc(func(data []byte) (int, error) { return 1, nil })
			}
			err := finishLimitsWorkspace(ctx, m, runErr, opts)
			if !errors.Is(err, want) || out.Len() != 0 {
				t.Fatalf("print bypassed an independent failure: output=%q err=%v", out.String(), err)
			}
			if scenario == "interrupt" {
				var interrupt *limitsInterrupt
				if !errors.As(err, &interrupt) || interrupt.ExitCode() != 130 {
					t.Fatal("print changed the interrupt exit status")
				}
			}
			if scenario == "print failure" || scenario == "short print" {
				if !strings.Contains(err.Error(), "Could not print the limits command") {
					t.Fatalf("print failure lacks useful recovery: %v", err)
				}
			}
		})
	}
}

func TestLimitsWorkspacePrintPreservesSourceFailures(t *testing.T) {
	for _, source := range []string{"costs", "first-cap-404", "costs-with-receipt"} {
		for _, printFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/print-fails-%t", source, printFails), func(t *testing.T) {
				m, backend := newLimitsController(t, 40, 12)
				var sourceErr error = errors.New("synthetic Costs failure")
				resource := "costs"
				if source == "first-cap-404" {
					sourceErr = &openai.Error{StatusCode: http.StatusNotFound}
					resource = "spend_limit"
					m.snapshot.LimitAbsent, m.snapshot.LimitUnconfirmed = false, true
				}
				m.snapshot.Errors = []limitsSourceFailure{{Resource: resource, Err: sourceErr}}
				limitsControllerReview(t, m)
				if source == "first-cap-404" && (!m.change.BeforeMissing || !strings.Contains(m.details, "creates or replaces")) {
					t.Fatal("first-cap review lost its explicit uncertainty warning")
				}
				_, quit := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				if quit == nil || m.commandToPrint == "" {
					t.Fatal("a nonfatal source failure removed the print action")
				}
				if source == "costs-with-receipt" {
					m.lastSaved = gjson.Parse("{\"object\":\"organization.spend_limit\",\"threshold_amount\":10001,\"currency\":\"USD\",\"interval\":\"month\"}")
					m.lastSavedOperation = limitsChangeOperation("limit_set", false)
				}
				var out bytes.Buffer
				opts := ShowJSONOpts{Context: t.Context(), Format: "text", Stdout: &out, Stderr: io.Discard}
				if printFails {
					opts.Stdout = limitsFailingWriter{err: syscall.EPIPE}
				}
				err := finishLimitsWorkspace(t.Context(), m, nil, opts)
				if !errors.Is(err, sourceErr) || backend.writes.Load() != 0 {
					t.Fatalf("printing hid the source failure or sent a mutation: %v", err)
				}
				if printFails {
					if !errors.Is(err, syscall.EPIPE) || clioutput.IsBrokenPipe(err) || out.Len() != 0 {
						t.Fatalf("print failure masked the independent source failure: %v", err)
					}
					return
				}
				if source == "costs-with-receipt" {
					if !strings.HasSuffix(out.String(), m.commandToPrint+"\n") || !strings.Contains(strings.TrimSuffix(out.String(), m.commandToPrint+"\n"), "USD 100.01") {
						t.Fatalf("source failure lost the confirmed receipt or complete command: %q", out.String())
					}
				} else if out.String() != m.commandToPrint+"\n" {
					t.Fatalf("source failure suppressed or changed the command: %q", out.String())
				}
			})
		}
	}
}

func TestLimitsWorkspacePrintPreservesConfirmedReceipt(t *testing.T) {
	for _, scenario := range []string{"confirmed result", "receipt failure", "confirmed removal"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := newLimitsController(t, 40, 12)
			limitsControllerReview(t, m)
			m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			m.lastSaved = gjson.Parse("{\"object\":\"organization.spend_limit\",\"threshold_amount\":10001,\"currency\":\"USD\",\"interval\":\"month\"}")
			m.lastSavedOperation = limitsChangeOperation("limit_set", false)
			if scenario == "confirmed removal" {
				m.lastSaved = gjson.Parse("{\"object\":\"organization.spend_limit.deleted\",\"deleted\":true}")
				m.lastSavedOperation = limitsChangeOperation("limit_delete", false)
				m.snapshot.LimitUnconfirmed = true
				m.snapshot.Errors = []limitsSourceFailure{{Resource: "spend_limit", Err: &openai.Error{StatusCode: http.StatusNotFound}}}
			}
			var out bytes.Buffer
			opts := ShowJSONOpts{Context: t.Context(), Format: "text", Stdout: &out, Stderr: io.Discard}
			if scenario == "receipt failure" {
				opts.Stdout = limitsFailingWriter{err: syscall.EPIPE}
			}
			err := finishLimitsWorkspace(t.Context(), m, nil, opts)
			if scenario == "receipt failure" {
				if !errors.Is(err, syscall.EPIPE) || out.Len() != 0 {
					t.Fatalf("print ignored a receipt failure: %v", err)
				}
				return
			}
			if err != nil || !strings.HasSuffix(out.String(), m.commandToPrint+"\n") {
				t.Fatalf("confirmed receipt suppressed the command: %q err=%v", out.String(), err)
			}
			want := "USD 100.01"
			if scenario == "confirmed removal" {
				want = "Removal confirmed. Current hard-limit configuration was not returned (HTTP 404)."
			}
			if !strings.Contains(strings.TrimSuffix(out.String(), m.commandToPrint+"\n"), want) {
				t.Fatalf("printing lost the preceding confirmed result: %q", out.String())
			}
		})
	}
}

// This checks emitted PowerShell bytes; native PowerShell execution is separate.
func TestLimitsWorkspacePrintedPowerShellCommandRetainsCallOperator(t *testing.T) {
	for _, width := range []int{40, 80} {
		t.Run(fmt.Sprintf("columns-%d", width), func(t *testing.T) {
			m, backend := newLimitsController(t, width, 12)
			m.change, m.page, m.shell = limitsChange{Kind: "limit_set", Amount: 12525}, "review", "pwsh"
			m.previewExecutable = "C:\\Program Files\\" + strings.Repeat("long ", 20) + "Owner's Tools\\openai.exe"
			m.requestArgs = []string{"--organization=org owner's"}
			m.setReview()
			command := limitsControllerPrintedCommand(t, m)
			want := "& '" + strings.ReplaceAll(m.previewExecutable, "'", "''") + "' '--organization=org owner''s' admin organization spend-limit update --currency USD --interval month --threshold-amount 12525\n"
			if command != want || backend.writes.Load() != 0 {
				t.Fatalf("print changed PowerShell syntax or sent a mutation: %q", command)
			}
		})
	}
}

func limitsPreviewExecutable(t *testing.T, name string) (string, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "CLI preview's directory")
	require.NoError(t, os.Mkdir(directory, 0700))
	path := filepath.Join(directory, name)
	// This executable records shell arguments and stdin. It cannot call the API.
	const script = `#!/bin/sh
set -eu
printf '%s\000' "$@" > "$LIMITS_PREVIEW_ARGS"
printf '%s' "${OPENAI_UNTRUSTED_STDIN-unset}" > "$LIMITS_PREVIEW_PROTECTED"
/bin/cat > "$LIMITS_PREVIEW_STDIN"
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0700))
	return path, directory
}

type limitsPreviewCapture struct {
	args      []string
	stdin     []byte
	protected string
}

func executeLimitsPreview(t *testing.T, shell, command, pathDirectory string) limitsPreviewCapture {
	t.Helper()
	path := "/bin/" + shell
	if _, err := os.Stat(path); err != nil {
		if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ","+shell+",") {
			t.Fatalf("required native shell %s is unavailable", shell)
		}
		t.Skipf("native %s is unavailable", shell)
	}
	directory := t.TempDir()
	argsPath, stdinPath, protectedPath := filepath.Join(directory, "argv"), filepath.Join(directory, "stdin"), filepath.Join(directory, "protected")
	args := []string{"--noprofile", "--norc", "-c", command}
	if shell == "zsh" {
		args = []string{"-f", "-c", command}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, path, args...)
	process.WaitDelay = 250 * time.Millisecond
	process.Dir = t.TempDir()
	process.Env = []string{"PATH=" + pathDirectory + ":/usr/bin:/bin", "LANG=en_US.UTF-8",
		"LIMITS_PREVIEW_ARGS=" + argsPath, "LIMITS_PREVIEW_STDIN=" + stdinPath,
		"LIMITS_PREVIEW_PROTECTED=" + protectedPath}
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	require.NoError(t, process.Run(), "shell=%s stderr=%q", shell, stderr.String())
	require.NoError(t, ctx.Err())
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())
	rawArgs, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(rawArgs, []byte{0}))
	input, err := os.ReadFile(stdinPath)
	require.NoError(t, err)
	protected, err := os.ReadFile(protectedPath)
	require.NoError(t, err)
	return limitsPreviewCapture{strings.Split(string(rawArgs[:len(rawArgs)-1]), "\x00"), input, string(protected)}
}

func TestLimitsWorkspacePrintedCommandNativeShellPreservesArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash/Zsh preview execution uses a POSIX recorder; native Windows requires separate coverage")
	}
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, directory := limitsPreviewExecutable(t, strings.Repeat("long ", 20)+"limits'\nrecorder")
			marker := filepath.Join(t.TempDir(), "MUST_NOT_EXIST")
			subject := "Owner's \"alert\"; $(touch " + marker + "); `touch " + marker + "` $HOME \\path\nnext line\x00JSON string"
			channel, err := json.Marshal(map[string]any{"type": "email", "recipients": []string{"finance@example.test"}, "subject_prefix": subject})
			if err != nil {
				t.Fatal(err)
			}
			requestArgs := []string{"--organization=org owner's", "--project=proj root's", "--base-url=https://example.invalid/proxy/v1"}
			cases := []struct {
				name      string
				projectID string
				change    limitsChange
				want      []string
			}{
				{name: "organization-limit", change: limitsChange{Kind: "limit_set", Amount: 12525},
					want: []string{"admin", "organization", "spend-limit", "update", "--currency", "USD", "--interval", "month", "--threshold-amount", "12525"}},
				{name: "project-limit", projectID: "proj target's", change: limitsChange{Kind: "limit_set", Amount: 12525},
					want: []string{"admin", "organization", "projects", "spend-limit", "update", "--project-id", "proj target's", "--currency", "USD", "--interval", "month", "--threshold-amount", "12525"}},
				{name: "organization-limit-delete", change: limitsChange{Kind: "limit_delete"},
					want: []string{"admin", "organization", "spend-limit", "delete"}},
				{name: "project-limit-delete", projectID: "proj target's", change: limitsChange{Kind: "limit_delete"},
					want: []string{"admin", "organization", "projects", "spend-limit", "delete", "--project-id", "proj target's"}},
				{name: "alert-create", change: limitsChange{Kind: "alert_create", Amount: 7550, Recipients: []string{"finance@example.test"}, SubjectPrefixSet: true, SubjectPrefix: &subject},
					want: []string{"admin", "organization", "spend-alerts", "create", "--currency", "USD", "--interval", "month", "--threshold-amount", "7550", "--notification-channel", string(channel)}},
				{name: "alert-update", projectID: "proj target's", change: limitsChange{Kind: "alert_update", ID: "alert owner's", Amount: 15025, Recipients: []string{"finance@example.test"}, SubjectPrefixSet: true, SubjectPrefix: &subject},
					want: []string{"admin", "organization", "projects", "spend-alerts", "update", "--project-id", "proj target's", "--alert-id", "alert owner's", "--currency", "USD", "--interval", "month", "--threshold-amount", "15025", "--notification-channel", string(channel)}},
				{name: "alert-delete", projectID: "proj target's", change: limitsChange{Kind: "alert_delete", ID: "alert owner's\n\tend"},
					want: []string{"admin", "organization", "projects", "spend-alerts", "delete", "--project-id", "proj target's", "--alert-id", "alert owner's\n\tend"}},
				{name: "rate-update", projectID: "proj target's", change: limitsChange{Kind: "rate_update", ID: "rl owner's", RateValues: map[string]int64{"max_requests_per_1_minute": -1}},
					want: []string{"admin", "organization", "projects", "rate-limits", "update-rate-limit", "--project-id", "proj target's", "--rate-limit-id", "rl owner's", "--max-requests-per-1-minute", "-1"}},
				{name: "tokens-per-day", projectID: "proj target's", change: limitsChange{Kind: "rate_update", ID: "rl owner's", RateValues: map[string]int64{"max_tokens_per_1_day": 9007199254740993}},
					want: []string{"admin", "organization", "projects", "rate-limits", "update-rate-limit", "--project-id", "proj target's", "--rate-limit-id", "rl owner's", "--max-tokens-per-1-day", "9007199254740993"}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					for _, width := range []int{40, 80} {
						t.Run(fmt.Sprintf("columns-%d", width), func(t *testing.T) {
							m, backend := newLimitsController(t, width, 12)
							m.change, m.page, m.shell = tc.change, "review", shell
							m.snapshot.ProjectID, m.requestArgs, m.previewExecutable = tc.projectID, requestArgs, binary
							m.setReview()
							command := limitsControllerPrintedCommand(t, m)
							if backend.writes.Load() != 0 {
								t.Fatal("printing a command wrote to the backend")
							}
							capture := executeLimitsPreview(t, shell, command, directory)
							want := append(append([]string{}, requestArgs...), tc.want...)
							if !reflect.DeepEqual(capture.args, want) {
								t.Fatalf("printed command changed arguments:\n got: %q\nwant: %q", capture.args, want)
							}
							if len(capture.stdin) != 0 || capture.protected != "unset" {
								t.Fatal("printed command unexpectedly supplied stdin or changed its environment")
							}
							if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
								t.Fatalf("printed command executed subject text: %v", err)
							}
						})
					}
				})
			}
		})
	}
}

func TestLimitsWorkspaceControlCFromEveryState(t *testing.T) {
	for _, page := range []string{"home", "form", "review", "details", "busy"} {
		t.Run(page, func(t *testing.T) {
			m, b := newLimitsController(t, 40, 12)
			var pending tea.Cmd
			if page == "busy" {
				b.release = make(chan struct{})
				change := limitsChange{Kind: "limit_set", Amount: 10001, BeforeMissing: true}
				pending = m.launch(&change)
				limitsControllerStarted(t, m, b)
			} else {
				m.page = page
			}
			_, quit := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if quit == nil || !m.quitting || !m.interrupted || !errors.Is(m.ctx.Err(), context.Canceled) || m.View().Content != "" {
				t.Fatal("Ctrl+C did not stop work and clear the owned view")
			}
			if pending != nil && !errors.Is(limitsControllerRun(t, pending).err, context.Canceled) {
				t.Fatal("Ctrl+C did not cancel the pending backend request")
			}
			m.work.Wait()
		})
	}
}

func TestLimitsWorkspacePreflightFailureRequiresRefresh(t *testing.T) {
	m, b := newLimitsController(t, 80, 24)
	b.mutationErr = &limitsMutationFailure{Err: errors.New("synthetic preflight conflict"), Attempted: false}
	limitsControllerReview(t, m)
	m.viewport.GotoBottom()
	limitsControllerKey(m, tea.KeyTab)
	m.Update(limitsControllerRun(t, limitsControllerKey(m, tea.KeyEnter)))
	limitsControllerStarted(t, m, b)
	if m.locked || !m.refreshRequired || m.page != "form" || m.draft.inputs[0].Value() != "100.01" {
		t.Fatal("preflight conflict did not retain a refreshable draft")
	}
	m.setFocus(len(m.draft.inputs))
	limitsControllerKey(m, tea.KeyEnter)
	if m.page != "form" || m.busy || b.writes.Load() != 1 {
		t.Fatal("preflight conflict allowed review before refresh")
	}
	b.snapshot.LimitAbsent = false
	b.snapshot.Limit = gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":20000,"currency":"USD","interval":"month","enforcement":{"status":"enforcing"}}`)
	m.Update(limitsControllerRun(t, limitsControllerKey(m, 'r')))
	if m.refreshRequired || m.locked || m.draft.inputs[0].Value() != "100.01" || m.draft.change.Before.Raw != b.snapshot.Limit.Raw || m.draft.change.BeforeMissing {
		t.Fatal("refresh discarded user input or failed to reconcile the baseline")
	}
	limitsControllerKey(m, tea.KeyEnter)
	if m.page != "review" || m.apply || m.change.Before.Raw != b.snapshot.Limit.Raw || m.change.Amount != 10001 {
		t.Fatal("refreshed review lost old/new distinction or selected Apply")
	}
	b.mutationErr = nil
	m.viewport.GotoBottom()
	limitsControllerKey(m, tea.KeyTab)
	limitsControllerRun(t, limitsControllerKey(m, tea.KeyEnter))
	change := limitsControllerStarted(t, m, b)
	if b.writes.Load() != 2 || change.Before.Raw != b.snapshot.Limit.Raw || change.Amount != 10001 {
		t.Fatal("explicit retry used a stale baseline or changed the draft")
	}
}

func TestLimitsWorkspaceAttemptedFailureStaysLockedAfterRefresh(t *testing.T) {
	m, b := newLimitsController(t, 80, 24)
	b.mutationErr = &limitsMutationFailure{Err: errors.New("synthetic uncertain response"), Attempted: true}
	limitsControllerReview(t, m)
	m.viewport.GotoBottom()
	limitsControllerKey(m, tea.KeyTab)
	m.Update(limitsControllerRun(t, limitsControllerKey(m, tea.KeyEnter)))
	limitsControllerStarted(t, m, b)
	if !m.locked || m.refreshRequired {
		t.Fatal("attempted write failure became a safe preflight rejection")
	}
	limitsControllerKey(m, tea.KeyEscape)
	m.Update(limitsControllerRun(t, limitsControllerKey(m, 'r')))
	limitsControllerKey(m, tea.KeyEnter)
	m.work.Wait()
	if !m.locked || m.page != "home" || b.writes.Load() != 1 {
		t.Fatal("refresh silently unlocked an uncertain write")
	}
}

func TestLimitsWorkspaceRefreshClearsStaleStatusAndRewrapsDetails(t *testing.T) {
	m, _ := newLimitsController(t, 80, 24)
	m.Update(limitsWorkResult{snapshot: limitsSnapshot{Errors: []limitsSourceFailure{{Resource: "spend_limit", Err: errors.New("synthetic failure")}}}, err: errors.New("synthetic failure")})
	if !strings.Contains(m.note, "failed") {
		t.Fatal("partial load did not report failure")
	}
	m.Update(limitsWorkResult{snapshot: limitsSnapshot{LimitAbsent: true, AlertsComplete: true}})
	if strings.Contains(m.note, "failed") {
		t.Fatal("successful refresh retained stale failure status")
	}
	m.page = "details"
	m.setDetails(strings.Repeat("界", 60))
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	view := ansi.Strip(m.View().Content)
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Errorf("resized details overflowed display cells: %q", line)
		}
	}
	if !strings.Contains(view, "Ctrl+C") {
		t.Fatal("resized details hid cancellation")
	}
}

func TestLimitsWorkspaceNoLimitPreservesOtherEditBaselines(t *testing.T) {
	for _, kind := range []string{"alert_update", "alert_delete", "rate_update"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := newLimitsController(t, 80, 24)
			value := gjson.Parse(limitsControllerAlert)
			if kind == "rate_update" {
				value = gjson.Parse(limitsControllerRate)
			}
			m.begin(kind, value)
			if m.change.Kind != kind || m.change.BeforeMissing || m.change.Before.Raw != value.Raw || m.change.ID != value.Get("id").Str {
				t.Fatalf("absent hard limit corrupted %s baseline: %+v", kind, m.change)
			}
		})
	}
}

func TestLimitsDraftExactAmountsAndPrefixPresence(t *testing.T) {
	for _, tc := range []struct {
		kind, amount string
		want         int64
		valid        bool
	}{
		{"limit_set", "92233720368547758.07", math.MaxInt64, true},
		{"limit_set", "0.01", 1, true}, {"limit_set", "0", 0, false},
		{"alert_create", "0", 0, true}, {"alert_create", "1.001", 0, false},
		{"limit_set", "92233720368547758.08", 0, false},
	} {
		draft, err := newLimitsDraft(limitsChange{Kind: tc.kind, BeforeMissing: true})
		if err != nil {
			t.Fatal(err)
		}
		draft.inputs[0].SetValue(tc.amount)
		if tc.kind == "alert_create" {
			draft.inputs[1].SetValue("synthetic@example.invalid")
		}
		change, err := draft.validate()
		if (err == nil) != tc.valid || tc.valid && change.Amount != tc.want {
			t.Errorf("%s amount %q: cents=%d error=%v", tc.kind, tc.amount, change.Amount, err)
		}
	}
	for _, tc := range []struct {
		name, original, value string
		mode                  int
		set, null             bool
	}{
		{name: "omitted remains omitted"},
		{name: "null remains null", original: `,"subject_prefix":null`, set: true, null: true},
		{name: "empty remains empty", original: `,"subject_prefix":""`, set: true},
		{name: "preserve string", original: `,"subject_prefix":"Existing"`, value: "Existing", set: true},
		{name: "replace empty", original: `,"subject_prefix":"Existing"`, mode: 1, set: true},
		{name: "clear null", original: `,"subject_prefix":"Existing"`, mode: 2, set: true, null: true},
		{name: "edited value", value: "New prefix", set: true},
		{name: "edited original", original: `,"subject_prefix":"Existing"`, value: "New prefix", set: true},
		{name: "edited original to empty", original: `,"subject_prefix":"Existing"`, set: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(limitsControllerAlert, `["synthetic@example.invalid"]}`, `["synthetic@example.invalid"]`+tc.original+`}`, 1)
			draft, err := newLimitsDraft(limitsChange{Kind: "alert_update", ID: "alert_synthetic", Before: gjson.Parse(raw)})
			if err != nil {
				t.Fatal(err)
			}
			draft.prefixMode = tc.mode
			draft.inputs[2].SetValue(tc.value)
			change, err := draft.validate()
			if err != nil || change.SubjectPrefixSet != tc.set || tc.set && (change.SubjectPrefix == nil) != tc.null {
				t.Fatalf("prefix presence changed: %+v; error=%v", change, err)
			}
			if tc.set && !tc.null && *change.SubjectPrefix != tc.value {
				t.Errorf("prefix content changed: %q", *change.SubjectPrefix)
			}
		})
	}
}

func TestLimitsDraftRatesSendOnlyExplicitChanges(t *testing.T) {
	draft, err := newLimitsDraft(limitsChange{Kind: "rate_update", ID: "rl_synthetic", Before: gjson.Parse(limitsControllerRate)})
	if err != nil {
		t.Fatal(err)
	}
	if draft.inputs[2].Value() != "" || draft.inputs[3].Value() != "0" || draft.inputs[4].Value() != "" {
		t.Fatal("draft conflated missing rates with explicit zero")
	}
	draft.inputs[0].SetValue("") // Blank retains the existing value.
	draft.inputs[1].SetValue("201")
	draft.inputs[3].SetValue("1")
	change, err := draft.validate()
	want := map[string]int64{"max_tokens_per_1_minute": 201, "max_images_per_1_minute": 1}
	if err != nil || !reflect.DeepEqual(change.RateValues, want) {
		t.Fatalf("rate changes=%v error=%v; want %v", change.RateValues, err, want)
	}
	for _, tc := range []struct {
		text  string
		value int64
	}{{"0", 0}, {"9223372036854775807", math.MaxInt64}} {
		draft.inputs[1].SetValue(tc.text)
		change, err := draft.validate()
		if err != nil || change.RateValues["max_tokens_per_1_minute"] != tc.value {
			t.Errorf("nonnegative rate %q was not preserved: %v, %v", tc.text, change.RateValues, err)
		}
	}
	for _, invalid := range []string{"-1", "-9223372036854775808", "-", "1.5", "1e3", "9223372036854775808", "-9223372036854775809"} {
		draft.inputs[1].SetValue(invalid)
		if _, err := draft.validate(); err == nil {
			t.Errorf("rate draft accepted %q", invalid)
		}
	}
}

func TestLimitsWorkspaceActionVisibleAtMinimumSize(t *testing.T) {
	for _, kind := range []string{"limit_set", "alert_create", "rate_update"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := newLimitsController(t, 40, 12)
			value := gjson.Result{}
			if kind == "rate_update" {
				value = gjson.Parse(limitsControllerRate)
			}
			m.begin(kind, value)
			for focus := 0; focus <= len(m.draft.inputs); focus++ {
				m.setFocus(focus)
				view := ansi.Strip(m.View().Content)
				if !strings.Contains(view, "Review change") || !strings.Contains(view, "Ctrl+C") {
					t.Errorf("%s focus %d hides action or cancel: %q", kind, focus, view)
				}
				if lines := strings.Split(view, "\n"); len(lines) > 12 {
					t.Errorf("minimum view uses %d rows", len(lines))
				} else {
					for _, line := range lines {
						if ansi.StringWidth(line) > 40 {
							t.Errorf("minimum view exceeds 40 columns: %q", line)
						}
					}
				}
			}
			if kind == "rate_update" {
				m.draft.inputs[0].SetValue("101")
			} else {
				m.draft.inputs[0].SetValue("100.01")
				if kind == "alert_create" {
					m.draft.inputs[1].SetValue("synthetic@example.invalid")
					m.draft.inputs[2].SetValue("Edited prefix")
					if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Prefix: Keep unless edited") {
						t.Fatalf("edited prefix has misleading mode guidance: %q", view)
					}
				}
			}
			m.review()
			if m.page != "review" {
				t.Fatalf("fixture could not enter review: %s", m.formError)
			}
			if kind == "alert_create" && (!m.change.SubjectPrefixSet || m.change.SubjectPrefix == nil || *m.change.SubjectPrefix != "Edited prefix") {
				t.Fatalf("review did not retain the edited prefix: %+v", m.change)
			}
			m.viewport.GotoBottom()
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "> Back") || !strings.Contains(view, "Apply change") || !strings.Contains(view, "Ctrl+C") {
				t.Errorf("minimum review hides confirmation: %q", view)
			}
		})
	}
}

func TestLimitsWorkspaceHelpRestoresDraftAndReview(t *testing.T) {
	t.Run("form", func(t *testing.T) {
		m, backend := newLimitsController(t, 40, 12)
		m.begin("alert_update", gjson.Parse(limitsControllerAlert))
		values := []string{"123.45", "finance@example.test", "Edited $HOME prefix"}
		for i, value := range values {
			m.draft.inputs[i].SetValue(value)
			m.draft.inputs[i].SetCursor(i + 1)
		}
		for mode := 0; mode < 3; mode++ {
			m.draft.prefixMode = mode
			for focus := 0; focus < 5; focus++ {
				m.setFocus(focus)
				before, err := m.draft.validate()
				require.NoError(t, err)
				for _, key := range []rune{tea.KeyF1, tea.KeyF1, '?', tea.KeyEscape} {
					require.Nil(t, limitsControllerKey(m, key))
				}
				require.Equal(t, "form", m.page)
				require.Equal(t, focus, m.focus)
				require.Equal(t, mode, m.draft.prefixMode)
				for i, value := range values {
					require.Equal(t, value, m.draft.inputs[i].Value())
					require.Equal(t, i == focus, m.draft.inputs[i].Focused())
					require.Equal(t, i+1, m.draft.inputs[i].Position())
				}
				after, err := m.draft.validate()
				require.NoError(t, err)
				require.Equal(t, limitsChangeBody(before), limitsChangeBody(after))
			}
		}
		require.Zero(t, backend.writes.Load())
		require.Zero(t, backend.reads.Load())
	})
	for _, resized := range []bool{false, true} {
		t.Run(fmt.Sprintf("review_resize_%t", resized), func(t *testing.T) {
			m, backend := newLimitsController(t, 80, 24)
			limitsControllerReview(t, m)
			command := m.previewCommand
			m.viewport.GotoBottom()
			review := m.viewport.View()
			limitsControllerKey(m, tea.KeyTab)
			require.True(t, m.apply)
			limitsControllerKey(m, tea.KeyF1)
			limitsControllerKey(m, tea.KeyF1)
			if resized {
				m.Update(tea.WindowSizeMsg{Width: 40, Height: 13})
			}
			limitsControllerKey(m, tea.KeyEscape)
			require.Equal(t, "review", m.page)
			require.False(t, m.apply)
			require.Equal(t, command, m.previewCommand)
			require.Equal(t, "100.01", m.draft.inputs[0].Value())
			if resized {
				require.Zero(t, m.viewport.YOffset())
				require.False(t, m.viewport.AtBottom())
				limitsControllerKey(m, tea.KeyTab)
				require.False(t, m.apply, "resize must require rereading before Apply")
			} else {
				require.Equal(t, review, m.viewport.View())
				require.True(t, m.viewport.AtBottom())
			}
			require.Zero(t, backend.writes.Load())
		})
	}
	t.Run("home_and_inspection", func(t *testing.T) {
		m, backend := newLimitsController(t, 40, 12)
		m.snapshot.Alerts = []gjson.Result{gjson.Parse(limitsControllerAlert)}
		m.section, m.selection = 1, 1
		for _, key := range []rune{tea.KeyF1, tea.KeyF1, tea.KeyEscape, 'i', tea.KeyF1, tea.KeyEscape} {
			require.Nil(t, limitsControllerKey(m, key))
		}
		require.Equal(t, "home", m.page)
		require.Equal(t, 1, m.section)
		require.Equal(t, 1, m.selection)
		require.False(t, m.quitting)
		require.Zero(t, backend.writes.Load())
	})
}

func TestLimitsWorkspaceReviewEscapeMatchesBack(t *testing.T) {
	for _, key := range []rune{tea.KeyEscape, tea.KeyEnter} {
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			m, backend := newLimitsController(t, 40, 13)
			m.begin("alert_update", gjson.Parse(limitsControllerAlert))
			m.draft.inputs[0].SetValue("125.25")
			m.draft.inputs[2].SetValue("Changed prefix")
			m.draft.prefixMode = 1
			m.setFocus(4)
			limitsControllerKey(m, tea.KeyEnter)
			require.Equal(t, "review", m.page)
			require.False(t, m.apply)
			if key == tea.KeyEscape {
				m.viewport.GotoBottom()
				limitsControllerKey(m, tea.KeyTab)
				require.True(t, m.apply)
			}
			limitsControllerKey(m, key)
			require.Equal(t, "form", m.page)
			require.Equal(t, 4, m.focus)
			require.Equal(t, 1, m.draft.prefixMode)
			require.Equal(t, "125.25", m.draft.inputs[0].Value())
			require.Equal(t, "Changed prefix", m.draft.inputs[2].Value())
			require.False(t, m.apply)
			limitsControllerKey(m, tea.KeyEnter)
			require.Equal(t, "review", m.page)
			require.False(t, m.apply)
			require.Equal(t, int64(12525), m.change.Amount)
			require.Equal(t, "Changed prefix", *m.change.SubjectPrefix)
			require.Zero(t, backend.writes.Load())
		})
	}
}

func TestLimitsWorkspaceFormDiagnosticsFitAndRemainAccessible(t *testing.T) {
	for _, width := range []int{40, 80} {
		for _, height := range []int{12, 13, 14, 24} {
			for _, state := range []string{"validation", "preflight", "uncertain"} {
				t.Run(fmt.Sprintf("%dx%d_%s", width, height, state), func(t *testing.T) {
					m, backend := newLimitsController(t, width, height)
					m.begin("alert_create", gjson.Result{})
					m.draft.inputs[0].SetValue("125.25")
					m.draft.inputs[1].SetValue("Name <finance@example.test>")
					m.review()
					require.Equal(t, "form", m.page)
					require.Contains(t, m.formError, "without display names")
					if state != "validation" {
						m.draft.inputs[1].SetValue("finance@example.test")
						m.review()
						m.mutationError = &limitsMutationFailure{
							Attempted: state == "uncertain",
							Err:       &openai.Error{StatusCode: 400, Message: "synthetic-private-response"},
						}
						m.Update(limitsWorkResult{mutation: true, err: m.mutationError})
					}
					outcome := strings.SplitN(m.formError, "\n", 2)[0]
					for focus := 0; focus < 5; focus++ {
						m.setFocus(focus)
						for _, dark := range []bool{false, true} {
							m.color, m.dark = false, dark
							raw := m.View().Content
							var monochrome bytes.Buffer
							writer := colorprofile.Writer{Forward: &monochrome, Profile: colorprofile.NoTTY}
							_, err := io.WriteString(&writer, raw)
							require.NoError(t, err)
							view := monochrome.String()
							require.Contains(t, strings.Join(strings.Fields(view), " "), outcome)
							for _, control := range []string{"Review change", "F1: help", "Esc: back", "Ctrl+C"} {
								require.Contains(t, view, control)
							}
							if state == "preflight" {
								require.Contains(t, view, "r: refresh")
							}
							require.NotContains(t, view, "\x1b")
							require.NotContains(t, view, "synthetic-private-response")
							require.LessOrEqual(t, len(strings.Split(view, "\n")), height)
							for _, line := range strings.Split(view, "\n") {
								require.LessOrEqual(t, ansi.StringWidth(line), width)
							}
						}
					}
					limitsControllerKey(m, tea.KeyF1)
					require.Equal(t, "details", m.page)
					require.NotContains(t, m.details, "synthetic-private-response")
					if state == "validation" {
						require.Contains(t, m.details, outcome)
					} else if state == "preflight" {
						require.Contains(t, m.details, outcome)
						require.Contains(t, m.details, "request fields and scope")
					} else {
						require.Contains(t, m.details, "It may have succeeded")
					}
					m.viewport.GotoBottom()
					require.True(t, m.viewport.AtBottom())
					limitsControllerKey(m, tea.KeyEscape)
					require.Equal(t, "form", m.page)
					require.Equal(t, 4, m.focus)
					require.Equal(t, "125.25", m.draft.inputs[0].Value())
					require.Zero(t, backend.writes.Load())
				})
			}
		}
	}
}

func TestLimitsWorkspaceListStatusStaysVisible(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete_%t", complete), func(t *testing.T) {
			m, backend := newLimitsController(t, 40, 12)
			m.snapshot.ProjectID = "proj_synthetic"
			m.snapshot.Alerts = []gjson.Result{gjson.Parse(limitsControllerAlert)}
			m.snapshot.Rates = []gjson.Result{gjson.Parse(limitsControllerRate)}
			m.snapshot.AlertsComplete, m.snapshot.RatesComplete = complete, complete
			m.snapshot.CostsTotal, m.snapshot.CostsComplete = "1.23", false
			for _, section := range []int{1, 2} {
				m.section = section
				status, label, tab := "Incomplete.", "Alerts loaded: 1", "> Alerts"
				if complete {
					status = "Complete."
				}
				if section == 2 {
					label, tab = "Rate records loaded: 1", "> Rates"
				}
				for _, colored := range []bool{false, true} {
					for _, dark := range []bool{false, true} {
						m.color, m.dark = colored, dark
						view := ansi.Strip(m.View().Content)
						require.Contains(t, view, status+" "+label)
						require.Contains(t, view, tab)
						require.Contains(t, view, "Reported Costs (incomplete): USD 1.23")
						require.Contains(t, view, "q quit")
						require.NotContains(t, view, "true")
						require.NotContains(t, view, "false")
						require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
						for _, line := range strings.Split(view, "\n") {
							require.LessOrEqual(t, ansi.StringWidth(line), m.width)
						}
					}
				}
			}
			require.Zero(t, backend.writes.Load())
		})
	}
}

func TestLimitsWorkspaceReviewShowsExactDollarChanges(t *testing.T) {
	for _, tc := range []struct {
		name, kind, before, amount, previous, next string
	}{
		{"limit", "limit_set", limitsTestLimit, "125.25", "USD 100.00 / month", "USD 125.25 / month"},
		{"alert", "alert_update", limitsControllerAlert, "0.00", "USD 100.00 / month", "USD 0.00 / month"},
		{"new_alert", "alert_create", "", "0.01", "New alert", "USD 0.01 / month"},
		{"unknown_limit", "limit_set", "", "125.25", "Not confirmed", "USD 125.25 / month"},
		{"large_limit", "limit_set", strings.Replace(limitsTestLimit, "10000", "9223372036854775806", 1), "92233720368547758.07", "USD 92233720368547758.06 / month", "USD 92233720368547758.07 / month"},
		{"remove_limit", "limit_delete", limitsTestLimit, "", "USD 100.00 / month", "Remove this resource"},
		{"remove_alert", "alert_delete", limitsControllerAlert, "", "USD 100.00 / month", "Remove this resource"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, backend := newLimitsController(t, 40, 12)
			m.snapshot.LimitAbsent = tc.before == ""
			m.begin(tc.kind, gjson.Parse(tc.before))
			if len(m.draft.inputs) != 0 {
				m.draft.inputs[0].SetValue(tc.amount)
				if tc.kind == "alert_create" {
					m.draft.inputs[1].SetValue("finance@example.test")
				}
				action := len(m.draft.inputs)
				if action == 3 {
					action++
				}
				m.setFocus(action)
				limitsControllerKey(m, tea.KeyEnter)
			}
			require.Equal(t, "review", m.page, m.formError)
			require.False(t, m.apply)
			require.Contains(t, m.details, "Before: "+tc.previous)
			require.Contains(t, m.details, "After: "+tc.next)
			if tc.before != "" {
				require.Contains(t, m.details, tc.before)
			}
			if tc.amount != "" {
				require.Contains(t, m.previewCommand, fmt.Sprintf("--threshold-amount %d", m.change.Amount))
				require.Contains(t, m.details, fmt.Sprintf(`"threshold_amount": %d`, m.change.Amount))
			}
			require.Contains(t, m.details, "\n"+m.previewCommand+"\n")
			require.Zero(t, backend.writes.Load())
		})
	}
}

func TestLimitsDraftCommandPreviewPreservesPublicRoutes(t *testing.T) {
	for _, tc := range []struct {
		change limitsChange
		wants  []string
	}{
		{limitsChange{Kind: "limit_set", Amount: 10001, BeforeMissing: true}, []string{"spend-limit update", "--currency USD", "--threshold-amount 10001"}},
		{limitsChange{Kind: "alert_update", ID: "alert_synthetic", Amount: 10001, Recipients: []string{"synthetic@example.invalid"}}, []string{"spend-alerts update", "--alert-id alert_synthetic", "--currency USD"}},
		{limitsChange{Kind: "rate_update", ID: "rl_synthetic", RateValues: map[string]int64{"max_requests_per_1_minute": 0}}, []string{"rate-limits update-rate-limit", "--rate-limit-id rl_synthetic", "--max-requests-per-1-minute 0"}},
	} {
		preview := limitsReview(tc.change, "proj_synthetic", nil, false, "bash")
		for _, want := range append(tc.wants, "openai admin organization projects", "--project-id proj_synthetic") {
			if !strings.Contains(preview, want) {
				t.Errorf("preview missing %q: %q", want, preview)
			}
		}
		if tc.change.Kind != "rate_update" && limitsChangeBody(tc.change)["currency"] != "USD" {
			t.Error("review body changed API currency spelling")
		}
	}
}

func TestLimitsDraftPreviewNamesDirectCommandDifferences(t *testing.T) {
	change := limitsChange{Kind: "limit_set", Amount: 10001, BeforeMissing: true}
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			preview := limitsReview(change, "", nil, false, shell, "/synthetic path/openai")
			for _, want := range []string{"Command preview (inspection only)", "Ctrl+P prints the complete command", "skips the workspace preflight", "can retry automatically", "creates or replaces"} {
				if !strings.Contains(preview, want) {
					t.Errorf("preview omitted %q", want)
				}
			}
			if strings.Contains(preview, "Equivalent command") || strings.Contains(preview, "Not configured") {
				t.Fatal("preview claimed unsupported equivalence or confirmed absence")
			}
			command, _ := limitsCommand(change, "", nil, false, shell, "/synthetic path/openai")
			if shell == "pwsh" && !strings.HasPrefix(command, "& '/synthetic path/openai' ") {
				t.Fatalf("PowerShell preview cannot invoke the quoted executable: %q", command)
			}
		})
	}
	deletion := limitsReview(limitsChange{Kind: "limit_delete"}, "", nil, true, "bash")
	if strings.Contains(deletion, "does not preserve a disabled budget") || !strings.Contains(deletion, "does not guarantee soft-budget behavior") {
		t.Fatal("hard-limit removal claimed unverified soft-budget semantics")
	}
}

func TestLimitsWorkspaceUnknownLimitRequiresExplicitReview(t *testing.T) {
	m, b := newLimitsController(t, 80, 24)
	m.snapshot = limitsSnapshot{LimitUnconfirmed: true, Errors: []limitsSourceFailure{{Resource: "spend_limit", Err: &openai.Error{StatusCode: 404}}}}
	if rows := m.menu(); len(rows) != 1 || rows[0].kind != "limit_set" || !strings.Contains(m.sourceSummary(), "unknown") {
		t.Fatal("unknown limit lost its explicit create-or-replace path or became confirmed absence")
	}
	limitsControllerReview(t, m)
	if !m.change.BeforeMissing || m.apply || !strings.Contains(m.details, "The current limit could not be confirmed.") || b.writes.Load() != 0 {
		t.Fatal("unknown-baseline change bypassed its warning and Back default")
	}
	m.page = "home"
	m.snapshot.Errors = append(m.snapshot.Errors, limitsSourceFailure{Resource: "project", Err: errors.New("synthetic project failure")})
	if len(m.menu()) != 0 {
		t.Fatal("unverified project scope allowed a limit change")
	}
	m.snapshot = limitsSnapshot{Errors: []limitsSourceFailure{{Resource: "spend_limit", Err: &openai.Error{StatusCode: 401}}}}
	if len(m.menu()) != 0 {
		t.Fatal("an authentication failure allowed an assumed-absent limit change")
	}
}

func TestLimitsPreflightGuidancePreservesFailureScope(t *testing.T) {
	for _, tc := range []struct {
		status             int
		code, want, reject string
	}{
		{401, "", "help setup admin", "help setup\n"},
		{403, "", "Check Admin-key permissions", "invalid key"},
		{404, "", "was not returned", "not configured"},
		{429, "organization_spend_limit_exceeded", "help admin organization spend-limit retrieve", "Wait before"},
		{429, "project_spend_limit_exceeded", "help admin organization projects spend-limit retrieve", "Wait before"},
		{429, "credit_balance_exhausted", "billing or usage limit", "Rate limited"},
		{429, "unknown_synthetic_code", "Wait before refreshing", "spend limit was reached"},
	} {
		err := &openai.Error{StatusCode: tc.status, Code: tc.code, Message: "private synthetic response prose"}
		got := limitsPreflightHint(err, "custom-openai")
		if !strings.Contains(got, tc.want) || strings.Contains(got, tc.reject) || strings.Contains(got, err.Message) || !strings.Contains(got, "No write sent.") {
			t.Errorf("status=%d code=%s yielded unsafe or incorrect recovery: %q", tc.status, tc.code, got)
		}
		for _, hint := range []string{limitsReadHint(err, "custom-openai"), limitsMutationHint(err, "custom-openai")} {
			if !strings.Contains(hint, tc.want) || strings.Contains(hint, tc.reject) || strings.Contains(hint, err.Message) || strings.Contains(hint, "No write sent.") {
				t.Errorf("status=%d code=%s lost its read or mutation classification: %q", tc.status, tc.code, hint)
			}
		}
	}
}

func TestLimitsWorkspaceHTTPFailureRecovery(t *testing.T) {
	for _, status := range []int{400, 422, 409, 503} {
		for _, operation := range []string{"read", "preflight", "mutation"} {
			t.Run(fmt.Sprintf("%s/%d", operation, status), func(t *testing.T) {
				var writes, preflightReads atomic.Int32
				var rejectPreflight atomic.Bool
				backend := limitsTestBackend(t, "", 100, func(w http.ResponseWriter, r *http.Request) {
					preflight := r.Method == http.MethodGet && operation == "preflight" && rejectPreflight.Load() && strings.HasSuffix(r.URL.Path, "/spend_limit")
					if r.Method == http.MethodGet && (operation == "mutation" || operation == "preflight" && !preflight) {
						if strings.HasSuffix(r.URL.Path, "/spend_limit") {
							limitsTestWrite(t, w, limitsTestLimit)
						} else if strings.HasSuffix(r.URL.Path, "/costs") {
							limitsTestWrite(t, w, `{"object":"page","data":[],"has_more":false}`)
						} else {
							limitsTestWrite(t, w, `{"object":"list","data":[],"has_more":false}`)
						}
						return
					}
					if r.Method != http.MethodGet {
						writes.Add(1)
					}
					if preflight {
						preflightReads.Add(1)
					}
					w.WriteHeader(status)
					limitsTestWrite(t, w, `{"error":{"message":"synthetic-private-message\u001b[2J","param":"synthetic-private-param\nline","code":"synthetic-private-code\u001b]0;title\u0007"}}`)
				})
				m, _ := newLimitsController(t, 80, 24)
				m.backend = backend
				var result limitsWorkResult
				if operation == "read" {
					result = limitsControllerRun(t, m.load())
				} else {
					if operation == "preflight" {
						overview := limitsControllerRun(t, m.load())
						if overview.err != nil || len(overview.snapshot.Errors) != 0 || overview.snapshot.Limit.Raw != limitsTestLimit {
							t.Fatal("preflight fixture did not load a successful overview")
						}
						m.Update(overview)
						rejectPreflight.Store(true)
					} else {
						backend.client = openai.NewClient(append(backend.client.Options, option.WithMaxRetries(3))...)
						m.snapshot.LimitAbsent, m.snapshot.Limit = false, gjson.Parse(limitsTestLimit)
					}
					limitsControllerReview(t, m)
					m.viewport.GotoBottom()
					limitsControllerKey(m, tea.KeyTab)
					result = limitsControllerRun(t, limitsControllerKey(m, tea.KeyEnter))
					var attempted *limitsMutationFailure
					wantAttempted, wantWrites := operation == "mutation", int32(0)
					if wantAttempted {
						wantWrites = 1
					}
					if !errors.As(result.err, &attempted) || attempted.Attempted != wantAttempted || writes.Load() != wantWrites {
						t.Fatal("failed operation changed its attempted state or mutation count")
					}
				}
				m.Update(result)
				if operation == "preflight" {
					if m.locked || !m.refreshRequired || m.page != "form" || m.draft.inputs[0].Value() != "100.01" || m.change.Before.Raw != limitsTestLimit || m.change.Amount != 10001 {
						t.Fatal("HTTP preflight failure discarded the draft or changed its recovery state")
					}
					if !strings.Contains(m.formError, fmt.Sprintf("HTTP %d", status)) || strings.Contains(m.formError, "synthetic-private") || strings.Contains(m.formError, "\x1b") {
						t.Fatal("preflight form omitted the status or exposed response data")
					}
					m.setFocus(len(m.draft.inputs))
					if next := limitsControllerKey(m, tea.KeyEnter); next != nil || m.page != "form" || m.busy || !m.refreshRequired || preflightReads.Load() != 1 || writes.Load() != 0 {
						t.Fatal("HTTP preflight failure allowed review or another request before refresh")
					}
				}
				limitsControllerKey(m, tea.KeyF1)
				if !strings.Contains(m.details, fmt.Sprintf("HTTP %d", status)) || strings.Contains(m.details, "synthetic-private") || strings.Contains(m.details, "\x1b") {
					t.Fatal("recovery omitted the status or exposed response data")
				}
				if status == 400 || status == 422 {
					fields := "request fields and scope"
					if operation == "mutation" {
						fields = "reviewed fields"
					}
					if !strings.Contains(m.details, fields) {
						t.Fatal("validation recovery did not identify fields to recheck")
					}
				}
				if operation == "read" {
					if writes.Load() != 0 || !strings.Contains(m.details, "No change was attempted.") || strings.Contains(m.details, "It may have succeeded") {
						t.Fatal("read failure was presented as an attempted mutation")
					}
					return
				}
				if operation == "preflight" {
					if !strings.Contains(m.details, "No write sent.") || !strings.Contains(m.details, "press r to refresh") && !strings.Contains(m.details, "Press r to refresh") || strings.Contains(m.details, "It may have succeeded") {
						t.Fatal("preflight recovery lost safe refresh guidance or claimed a possible mutation")
					}
					if (status == 409 || status == 503) && !strings.Contains(m.details, "API availability and access") {
						t.Fatal("preflight recovery omitted availability and access guidance")
					}
				} else {
					if !m.locked || !strings.Contains(m.details, "It may have succeeded") || strings.Contains(m.details, "No write sent.") {
						t.Fatal("HTTP failure removed mutation uncertainty or the write lock")
					}
					limitsControllerKey(m, tea.KeyEscape)
					if m.page != "form" {
						t.Fatal("recovery help did not restore the locked draft")
					}
					limitsControllerKey(m, tea.KeyEscape)
					limitsControllerKey(m, tea.KeyEnter)
					m.Update(limitsControllerRun(t, limitsControllerKey(m, 'r')))
					limitsControllerKey(m, tea.KeyEnter)
					if !m.locked || writes.Load() != 1 {
						t.Fatal("Enter or refresh repeated a failed mutation")
					}
				}
				m.output = &limitsWorkspaceOutput{err: &clioutput.WriteError{Err: syscall.EPIPE}}
				failure := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: "text", Stdout: io.Discard, Stderr: io.Discard})
				if !errors.Is(failure, result.err) || !errors.Is(failure, syscall.EPIPE) || clioutput.IsBrokenPipe(failure) {
					t.Fatal("finalization discarded the operation or output failure")
				}
				baseline := "A limits change may have succeeded. Check its current state before repeating it.\n" + m.readRecovery()
				if operation == "preflight" {
					baseline = fmt.Sprintf("The API returned HTTP %d. No write sent.\n", status)
					if status == 400 || status == 422 {
						baseline += "Recheck the request fields and scope.\nReopen the limits workspace and review current settings before another change."
					} else {
						baseline += "Reopen the limits workspace and review current settings before another change. If this continues, check API availability and access."
					}
					baseline += "\n" + m.readRecovery()
				}
				for _, args := range [][]string{nil, {"--format", "json", "--format-error", "text"}, {"--format", "text", "--format-error", "json"}, {"--format-error", "text", "--transform-error", "message"}} {
					root := readableErrorTestCommand(t, args...)
					var out bytes.Buffer
					if err := ShowCommandError(root, failure, &out); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(out.String(), "synthetic-private") || strings.Contains(out.String(), "\x1b") {
						t.Fatal("final diagnostic exposed response data")
					}
					if strings.Contains(strings.ToLower(out.String()), "press r") {
						t.Fatal("final diagnostic directed the user to a closed workspace")
					}
					switch {
					case root.String("transform-error") != "":
						if out.String() != baseline+"\n" {
							t.Fatal("extracted error message changed")
						}
					case errorOutputFormat(root) == "json":
						var value map[string]any
						if err := json.Unmarshal(out.Bytes(), &value); err != nil || len(value) != 1 || value["message"] != baseline {
							t.Fatal("machine error message changed")
						}
					default:
						if operation == "preflight" {
							if !strings.Contains(out.String(), baseline) || strings.Contains(out.String(), "may have succeeded") {
								t.Fatal("final text lost preflight status, outcome, or recovery guidance")
							}
							break
						}
						if !strings.Contains(out.String(), fmt.Sprintf("HTTP %d", status)) || !strings.Contains(out.String(), "may have succeeded") {
							t.Fatal("final text lost HTTP status or mutation uncertainty")
						}
						if (status == 400 || status == 422) && !strings.Contains(out.String(), "reviewed fields") {
							t.Fatal("final text omitted validation recovery")
						}
					}
				}
			})
		}
	}
}

func TestLimitsWorkspaceExitRecoveryUsesProcessActions(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		cause      error
	}{
		{"authentication", "help setup admin", &openai.Error{StatusCode: 401, Message: "synthetic-private-response"}},
		{"access", "organization or project access", &openai.Error{StatusCode: 403, Message: "synthetic-private-response"}},
		{"missing", "scope, and resource ID", &openai.Error{StatusCode: 404, Message: "synthetic-private-response"}},
		{"rate", "Wait before reopening", &openai.Error{StatusCode: 429, Code: "unknown_synthetic_code", Message: "synthetic-private-response"}},
		{"organization cap", "help admin organization spend-limit retrieve", &openai.Error{StatusCode: 429, Code: "organization_spend_limit_exceeded"}},
		{"project cap", "help admin organization projects spend-limit retrieve", &openai.Error{StatusCode: 429, Code: "project_spend_limit_exceeded"}},
		{"billing", "https://platform.openai.com/settings/organization/limits", &openai.Error{StatusCode: 429, Code: "credit_balance_exhausted"}},
		{"canceled", "check was interrupted", context.Canceled},
		{"deadline", "check was interrupted", context.DeadlineExceeded},
		{"changed", "Record changed", errors.New("The configuration changed. Refresh before applying this change.")},
		{"incomplete rates", "Reopen with --max-items -1", errors.New("Rate-limit editing requires a complete list. Reopen with a larger --max-items value or -1.")},
		{"unknown", "Reopen the limits workspace", errors.New("synthetic-private-response\x1b[2J")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, backend := newLimitsController(t, 40, 12)
			m.invocation, m.snapshot.ProjectID = "custom-openai", "synthetic-private-project"
			limitsControllerReview(t, m)
			m.mutationError = &limitsMutationFailure{Err: tc.cause, Attempted: false}
			m.Update(limitsWorkResult{mutation: true, err: m.mutationError})
			limitsControllerKey(m, tea.KeyF1)
			if tc.name == "missing" || tc.name == "canceled" || tc.name == "deadline" || tc.name == "changed" || tc.name == "unknown" {
				if !strings.Contains(m.details, "Press r to refresh") {
					t.Fatal("in-workspace recovery lost its keyboard action")
				}
			}
			limitsControllerKey(m, tea.KeyEscape)
			if m.page != "form" {
				t.Fatal("recovery help did not restore the rejected draft")
			}
			limitsControllerKey(m, tea.KeyEscape)
			limitsControllerKey(m, 'q')
			m.lastSaved = gjson.Parse(strings.Replace(limitsTestLimit, "organization.spend_limit", "project.spend_limit", 1))
			m.lastSavedOperation = limitsChangeOperation("limit_set", true)
			var receipt bytes.Buffer
			failure := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: "json", ExplicitFormat: true, Stdout: &receipt, Stderr: io.Discard})
			var safe *limitsWorkflowFailure
			if !errors.As(failure, &safe) || !errors.Is(failure, tc.cause) || backend.writes.Load() != 0 {
				t.Fatal("final recovery lost the cause, failure, or zero-write outcome")
			}
			if !gjson.Valid(receipt.String()) || gjson.Get(receipt.String(), "threshold_amount").Int() != 10000 || !strings.Contains(safe.Error(), "A preceding change was confirmed.") {
				t.Fatal("final recovery discarded the preceding confirmed result")
			}
			for _, args := range [][]string{nil, {"--format-error", "json"}, {"--format-error", "text", "--transform-error", "message"}} {
				root := readableErrorTestCommand(t, args...)
				var out bytes.Buffer
				if err := ShowCommandError(root, failure, &out); err != nil {
					t.Fatal(err)
				}
				text := out.String()
				if strings.Contains(text, "synthetic-private") || strings.Contains(text, "\x1b") || strings.Contains(strings.ToLower(text), "press r") || strings.Contains(text, "may have succeeded") {
					t.Fatal("final recovery leaked private data, keyboard actions, or mutation uncertainty")
				}
				for _, want := range []string{tc.want, "custom-openai help admin organization projects spend-limit retrieve", "original connection, authentication, and scope options"} {
					if !strings.Contains(text, want) {
						t.Fatalf("final recovery omitted %q", want)
					}
				}
				if root.String("transform-error") != "" {
					if text != safe.Error()+"\n" {
						t.Fatal("extracted message differs from the canonical final recovery")
					}
				} else if errorOutputFormat(root) == "json" {
					var value map[string]any
					if err := json.Unmarshal(out.Bytes(), &value); err != nil || len(value) != 1 || value["message"] != safe.Error() {
						t.Fatal("machine error schema or message changed")
					}
				}
			}
		})
	}
	for _, status := range []int{404, 429} {
		t.Run(fmt.Sprintf("uncertain/%d", status), func(t *testing.T) {
			m, _ := newLimitsController(t, 80, 24)
			cause := &openai.Error{StatusCode: status, Message: "synthetic-private-response"}
			m.locked, m.mutationError = true, &limitsMutationFailure{Err: cause, Attempted: true}
			if status == 404 && !strings.Contains(m.help(), "Press r to refresh") {
				t.Fatal("in-workspace mutation recovery lost its keyboard guidance")
			}
			failure := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Stdout: io.Discard, Stderr: io.Discard})
			if !errors.Is(failure, cause) {
				t.Fatal("final mutation recovery lost its cause")
			}
			baseline := "A limits change may have succeeded. Check its current state before repeating it.\n" + m.readRecovery()
			var human, machine bytes.Buffer
			if err := ShowCommandError(readableErrorTestCommand(t), failure, &human); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.ToLower(human.String()), "press r") || strings.Contains(human.String(), "synthetic-private") || !strings.Contains(human.String(), baseline) {
				t.Fatal("final mutation recovery lost uncertainty or exposed an invalid action")
			}
			if status == 404 && !strings.Contains(human.String(), "Read the current record before another change.") {
				t.Fatal("uncertain 404 did not require a new read")
			}
			if err := ShowCommandError(readableErrorTestCommand(t, "--format-error", "json"), failure, &machine); err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(machine.Bytes(), &value); err != nil || len(value) != 1 || value["message"] != baseline {
				t.Fatal("final mutation recovery changed the machine contract")
			}
		})
	}
	t.Run("interrupted preflight and output failure", func(t *testing.T) {
		m, _ := newLimitsController(t, 80, 24)
		cause := &openai.Error{StatusCode: 404, Message: "synthetic-private-response"}
		m.refreshRequired, m.interrupted = true, true
		m.mutationError = &limitsMutationFailure{Err: errors.Join(cause, context.Canceled), Attempted: false}
		m.output = &limitsWorkspaceOutput{err: &clioutput.WriteError{Err: syscall.EPIPE}}
		failure := finishLimitsWorkspace(t.Context(), m, tea.ErrInterrupted, ShowJSONOpts{Context: t.Context(), Stdout: io.Discard, Stderr: io.Discard})
		var interrupt interface{ ExitCode() int }
		if !errors.Is(failure, cause) || !errors.Is(failure, context.Canceled) || !errors.Is(failure, syscall.EPIPE) || clioutput.IsBrokenPipe(failure) || !errors.As(failure, &interrupt) || interrupt.ExitCode() != 130 {
			t.Fatal("final preflight recovery lost a joined cause or interruption status")
		}
		var out bytes.Buffer
		if err := ShowCommandError(readableErrorTestCommand(t), failure, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "synthetic-private") || strings.Contains(strings.ToLower(out.String()), "press r") || !strings.Contains(out.String(), "No write sent.") || !strings.Contains(out.String(), "Reopen the limits workspace") {
			t.Fatal("interrupted final preflight recovery lost its process-safe action")
		}
	})
}

func TestLimitsWorkspaceCanceledMutationRecovery(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		m, _ := newLimitsController(t, 80, 24)
		m.locked, m.mutationError = true, &limitsMutationFailure{Err: cause, Attempted: true}
		failure := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: "text", Stdout: io.Discard, Stderr: io.Discard})
		var out bytes.Buffer
		if err := ShowCommandError(readableErrorTestCommand(t), failure, &out); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(failure, cause) || !strings.Contains(out.String(), "interrupted") || strings.Contains(out.String(), "Check the API address and connection") || !strings.Contains(out.String(), "may have succeeded") {
			t.Fatal("canceled mutation lost its cause or gave connection advice")
		}
	}
}

func TestLimitsWorkspaceRecoveryAccessibleFromLockedForm(t *testing.T) {
	m, _ := newLimitsController(t, 40, 12)
	m.begin("alert_create", gjson.Result{})
	m.locked = true
	m.change.Kind = "alert_create"
	m.formError = "Save not confirmed. Press ? for recovery. Exit before another change."
	if !strings.Contains(m.View().Content, "F1: help") {
		t.Fatal("minimum-size form hid the recovery action")
	}
	limitsControllerKey(m, tea.KeyF1)
	if m.page != "details" || !strings.Contains(m.details, "It may have succeeded") || !strings.Contains(m.details, "help admin organization spend-alerts list") {
		t.Fatal("recovery help did not explain uncertainty and a safe read")
	}
	if !strings.Contains(m.View().Content, "Ctrl+C") {
		t.Fatal("recovery help hid cancellation")
	}
}

type limitsFailingWriter struct{ err error }

func (w limitsFailingWriter) Write([]byte) (int, error) { return 0, w.err }

type limitsWorkspaceWriteFunc func([]byte) (int, error)

func (write limitsWorkspaceWriteFunc) Write(data []byte) (int, error) { return write(data) }

func TestLimitsWorkspaceOutputPreservesFailuresAndCleanup(t *testing.T) {
	for _, method := range []string{"Write", "WriteString"} {
		for _, failure := range []string{"sink error", "short write"} {
			t.Run(method+"/"+failure, func(t *testing.T) {
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				first, later := errors.New("synthetic output failure"), errors.New("later output failure")
				want := first
				if failure == "short write" {
					want = io.ErrShortWrite
				}
				calls, cleanup := 0, ""
				sink := limitsWorkspaceWriteFunc(func(data []byte) (int, error) {
					calls++
					switch calls {
					case 1:
						if failure == "short write" {
							return 2, nil
						}
						return 2, first
					case 2:
						cleanup = string(data)
						return len(data), nil
					default:
						return 0, later
					}
				})
				out := &limitsWorkspaceOutput{
					Writer: clioutput.Writer{Context: context.Background(), Sink: sink},
					stop:   stop}
				write := func(value string) (int, error) { return out.Write([]byte(value)) }
				if method == "WriteString" {
					write = func(value string) (int, error) { return io.WriteString(out, value) }
				}
				n, original := write("result")
				var writeErr *clioutput.WriteError
				if n != 2 || !errors.Is(original, want) || !errors.As(original, &writeErr) {
					t.Fatalf("lost partial write or sink classification: n=%d err=%v", n, original)
				}
				if !errors.Is(ctx.Err(), context.Canceled) || out.Err() != original {
					t.Fatal("output failure did not cancel the workspace and retain the original error")
				}
				const restore = "\x1b[?25h"
				if n, err := io.WriteString(out, restore); n != len(restore) || err != nil || cleanup != restore {
					t.Fatalf("workspace cancellation prevented terminal cleanup: n=%d err=%v", n, err)
				}
				if _, err := out.Write([]byte("later")); !errors.Is(err, later) || out.Err() != original {
					t.Fatal("a later write replaced the first output failure")
				}
			})
		}
	}
}

func TestLimitsWorkspaceOutputPreservesCancellation(t *testing.T) {
	for _, when := range []string{"before write", "during write"} {
		t.Run(when, func(t *testing.T) {
			outputContext, cancelOutput := context.WithCancel(t.Context())
			defer cancelOutput()
			workspaceContext, stop := context.WithCancel(t.Context())
			defer stop()
			calls := 0
			out := &limitsWorkspaceOutput{
				Writer: clioutput.Writer{Context: outputContext, Sink: limitsWorkspaceWriteFunc(func(data []byte) (int, error) {
					calls++
					cancelOutput()
					return len(data), nil
				})},
				stop: stop}
			wantBytes, wantCalls := len("result"), 1
			if when == "before write" {
				cancelOutput()
				wantBytes, wantCalls = 0, 0
			}
			n, err := io.WriteString(out, "result")
			var writeErr *clioutput.WriteError
			if n != wantBytes || calls != wantCalls || !errors.Is(err, context.Canceled) || errors.As(err, &writeErr) {
				t.Fatalf("cancellation changed byte counts or became a sink failure: n=%d calls=%d err=%v", n, calls, err)
			}
			if out.Err() != err || !errors.Is(workspaceContext.Err(), context.Canceled) {
				t.Fatal("cancellation did not stop the workspace and preserve its cause")
			}
		})
	}
}

func TestLimitsWorkspaceOutputPreservesTerminalFile(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("borrowed=%t", borrowed), func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "terminal-output")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { file.Close() })
			if _, err := file.WriteString("input"); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			var sink bytes.Buffer
			out := &limitsWorkspaceOutput{
				Writer: clioutput.Writer{Context: t.Context(), Sink: &sink}, File: file,
				stop: func() { t.Fatal("successful output canceled the workspace") }}
			if borrowed {
				writer := terminal.BorrowedWriter{Writer: &sink, File: file}
				out.Writer.Sink, out.File = writer, writer
			}
			if out.Fd() != file.Fd() {
				t.Fatal("output wrapper changed the descriptor used for terminal detection")
			}
			if _, err := io.WriteString(out, "output"); err != nil || sink.String() != "output" {
				t.Fatal("output wrapper bypassed its guarded sink", err)
			}
			input := make([]byte, len("input"))
			if _, err := io.ReadFull(out, input); err != nil || string(input) != "input" {
				t.Fatal("output wrapper changed terminal reads", err)
			}
			if out.Err() != nil {
				t.Fatal("successful writes recorded an error", out.Err())
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = file.Stat()
			if borrowed && err != nil || !borrowed && !errors.Is(err, os.ErrClosed) {
				t.Fatal("output wrapper changed terminal file ownership", err)
			}
		})
	}
}

func TestLimitsWorkspaceFinalizationPreservesIndependentFailures(t *testing.T) {
	for _, scenario := range []string{"read and terminal output", "mutation and terminal output", "cancellation and terminal output", "refresh and receipt output", "program and terminal output"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := newLimitsController(t, 80, 24)
			m.quitting = true
			ctx := t.Context()
			original := errors.New("private synthetic operation failure")
			writeErr := &clioutput.WriteError{Err: syscall.EPIPE}
			opts := ShowJSONOpts{Context: ctx, Stdout: io.Discard, Stderr: io.Discard, Format: "text"}
			var runErr error
			switch scenario {
			case "program and terminal output":
				runErr = errors.Join(original, tea.ErrProgramKilled)
				m.output = &limitsWorkspaceOutput{err: writeErr}
			case "read and terminal output":
				m.workerError = original
				m.output = &limitsWorkspaceOutput{err: writeErr}
			case "mutation and terminal output":
				m.mutationPending = true
				m.mutationError = original
				m.output = &limitsWorkspaceOutput{err: writeErr}
			case "cancellation and terminal output":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				original = context.Canceled
				m.interrupted = true
				m.output = &limitsWorkspaceOutput{err: writeErr}
			case "refresh and receipt output":
				m.snapshot.Errors = []limitsSourceFailure{{Resource: "spend_alerts", Err: original}}
				m.lastSaved = gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10001,"currency":"USD","interval":"month"}`)
				m.lastSavedOperation = limitsChangeOperation("limit_set", false)
				opts.Stdout = limitsFailingWriter{err: syscall.EPIPE}
			}
			err := finishLimitsWorkspace(ctx, m, runErr, opts)
			if !errors.Is(err, original) || !errors.Is(err, syscall.EPIPE) || clioutput.IsBrokenPipe(err) {
				t.Fatalf("output failure hid an independent outcome: %v", err)
			}
			var safe *limitsWorkflowFailure
			if !errors.As(err, &safe) || strings.Contains(safe.Error(), "private synthetic") || strings.Contains(safe.Error(), "shown above") {
				t.Fatal("finalization lost safe outcome guidance or asserted an undisplayed receipt")
			}
			if scenario == "cancellation and terminal output" {
				var interrupt *limitsInterrupt
				if !errors.As(err, &interrupt) || interrupt.ExitCode() != 130 {
					t.Fatal("output failure changed the interrupt status")
				}
			}
		})
	}
}

func TestLimitsWorkspaceConfirmedSaveSurvivesTerminalFailure(t *testing.T) {
	for _, kind := range []string{"limit_set", "alert_create", "limit_delete", "rate_update"} {
		for _, phase := range []string{"terminal", "receipt"} {
			for _, format := range []string{"text", "json"} {
				t.Run(kind+"/"+phase+"/"+format, func(t *testing.T) {
					m, _ := newLimitsController(t, 80, 24)
					m.workerSaved = gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10000,"currency":"USD","interval":"month"}`)
					if kind == "alert_create" {
						m.workerSaved = gjson.Parse(limitsControllerAlert)
					} else if kind == "limit_delete" {
						m.workerSaved = gjson.Parse(`{"object":"organization.spend_limit.deleted","deleted":true}`)
					} else if kind == "rate_update" {
						m.snapshot.ProjectID = "proj_synthetic"
						m.workerSaved = gjson.Parse(limitsControllerRate)
					}
					m.workerSavedOperation = limitsChangeOperation(kind, m.snapshot.ProjectID != "")
					m.change.Kind = "limit_set"
					if kind == "limit_set" {
						m.change.Kind = "alert_create"
					}
					var out bytes.Buffer
					var sink io.Writer = &out
					var runErr error = tea.ErrProgramKilled
					expectedFailure := "terminal output failed"
					m.output = &limitsWorkspaceOutput{err: &clioutput.WriteError{Err: syscall.EPIPE}}
					if phase == "receipt" {
						m.output = &limitsWorkspaceOutput{}
						sink = limitsFailingWriter{err: syscall.EPIPE}
						runErr = nil
						expectedFailure = "receipt could not be printed"
					}
					err := finishLimitsWorkspace(t.Context(), m, runErr, ShowJSONOpts{Context: t.Context(), Format: format, Stdout: sink, Stderr: io.Discard})
					var safe *limitsWorkflowFailure
					if !errors.As(err, &safe) || !errors.Is(err, syscall.EPIPE) || clioutput.IsBrokenPipe(err) {
						t.Fatalf("terminal failure hid the confirmed outcome: %v", err)
					}
					if !strings.Contains(safe.Error(), "change was confirmed") || !strings.Contains(safe.Error(), expectedFailure) || !strings.Contains(safe.Error(), "before another change") {
						t.Fatalf("missing confirmed-save recovery: %v", err)
					}
					readOptions := "help admin limits show"
					if m.snapshot.ProjectID != "" {
						readOptions = "help projects limits show"
					}
					if !strings.Contains(safe.Error(), readOptions) || strings.Contains(safe.Error(), "spend-limit retrieve") || strings.Contains(safe.Error(), "spend-alerts list") {
						t.Fatalf("unsent draft redirected confirmed-save recovery: %v", err)
					}
					if out.Len() != 0 || m.mutationPending || m.lastSaved.Raw != m.workerSaved.Raw {
						t.Fatal("finalization repeated output or lost the confirmed result")
					}
				})
			}
		}
	}
}

func TestLimitsWorkspaceReceiptUsesMutationProjection(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			m, _ := newLimitsController(t, 80, 24)
			m.quitting = true
			m.workerSaved = gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10001,"currency":"USD","interval":"month","enforcement":{"status":"inactive"}}`)
			m.workerSavedOperation = limitsChangeOperation("limit_set", false)
			var out bytes.Buffer
			err := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: format, ExplicitFormat: true, Stdout: &out, Stderr: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			if format == "text" {
				if !strings.Contains(out.String(), "USD 100.01") || !strings.Contains(out.String(), "inactive") {
					t.Fatalf("receipt lost threshold units or returned state: %q", out.String())
				}
			} else if !gjson.Valid(out.String()) || gjson.Get(out.String(), "threshold_amount").Raw != "10001" || strings.Contains(out.String(), "spend_threshold") {
				t.Fatalf("machine receipt changed threshold data: %q", out.String())
			}
		})
	}
}

func TestLimitsWorkspaceCostsAndEnforcementStayDistinct(t *testing.T) {
	m, _ := newLimitsController(t, 80, 24)
	m.snapshot.Limit = gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10001,"currency":"USD","interval":"month","enforcement":{"status":"inactive"}}`)
	m.snapshot.LimitAbsent = false
	m.snapshot.CostsTotal, m.snapshot.CostsComplete = "123.000000000000000001", false
	m.snapshot.CostsStart, m.snapshot.CostsEnd = 1790812800, 1792368000
	view := m.View().Content
	if !strings.Contains(view, "Reported Costs (incomplete): USD 123.000000000000000001") || !strings.Contains(view, "Returned enforcement: inactive") {
		t.Fatalf("workspace conflated Costs and returned enforcement: %q", view)
	}
	help := m.help()
	if !strings.Contains(help, "Queried period:") || !strings.Contains(help, "Today's Costs remain partial.") || !strings.Contains(help, "Costs can lag and do not determine enforcement state") {
		t.Fatal("Costs view omitted its coverage and enforcement limitations")
	}
	m.snapshot.CostsComplete = true
	view = m.View().Content
	if !strings.Contains(view, "Reported Costs: USD 123.000000000000000001") || strings.Contains(view, "(complete)") || strings.Contains(view, "(incomplete)") {
		t.Fatalf("complete pagination must not imply final Costs: %q", view)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	for _, complete := range []bool{false, true} {
		m.snapshot.CostsComplete = complete
		view = ansi.Strip(m.View().Content)
		if !complete && !strings.Contains(view, "Reported Costs (incomplete)") {
			t.Fatalf("narrow overview hid incomplete Costs: %q", view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > 40 {
				t.Fatalf("minimum-size overview exceeded its width: %q", line)
			}
		}
	}
}

func TestLimitsWorkspaceConfirmedRemovalPreservesReceiptAfter404(t *testing.T) {
	for _, otherFailure := range []bool{false, true} {
		m, _ := newLimitsController(t, 80, 24)
		m.quitting = true
		missing := &openai.Error{StatusCode: 404}
		m.lastSaved = gjson.Parse(`{"object":"organization.spend_limit.deleted","deleted":true}`)
		m.lastSavedOperation = limitsChangeOperation("limit_delete", false)
		m.snapshot = limitsSnapshot{LimitUnconfirmed: true, AlertsComplete: true, CostsComplete: true,
			Errors: []limitsSourceFailure{{Resource: "spend_limit", Err: missing}}}
		m.workerError = missing
		independent := errors.New("synthetic Costs failure")
		if otherFailure {
			m.snapshot.Errors = append(m.snapshot.Errors, limitsSourceFailure{Resource: "costs", Err: independent})
			m.workerError = errors.Join(missing, independent)
		}
		var out bytes.Buffer
		err := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: "text", Stdout: &out, Stderr: io.Discard})
		if otherFailure {
			if !errors.Is(err, independent) {
				t.Fatal("confirmed removal hid an independent refresh failure")
			}
		} else if err != nil || !strings.Contains(out.String(), "Removal confirmed. Current hard-limit configuration was not returned (HTTP 404).") {
			t.Fatalf("expected post-removal 404 overturned a confirmed removal: %v, %q", err, out.String())
		}
		if !strings.Contains(out.String(), "deleted") || strings.Contains(out.String(), "No hard spend limit configured") {
			t.Fatalf("receipt disappeared or 404 became confirmed absence: %q", out.String())
		}
	}
}

func TestLimitsWorkspaceShutdownKeepsCompletedSourceFailure(t *testing.T) {
	m, _ := newLimitsController(t, 80, 24)
	m.quitting = true
	original := errors.New("synthetic source failure before shutdown")
	snapshot := limitsSnapshot{Errors: []limitsSourceFailure{
		{Resource: "spend_limit", Err: original}, {Resource: "costs", Err: context.Canceled},
	}}
	m.workerSnapshot = &snapshot
	m.workerError = errors.Join(original, context.Canceled)
	err := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: "text", Stdout: io.Discard, Stderr: io.Discard})
	if !errors.Is(err, original) || errors.Is(err, context.Canceled) {
		t.Fatalf("normal shutdown hid the read failure or became an interruption: %v", err)
	}
}

func TestLimitsWorkspaceAmbiguousEnforcementStaysUnknown(t *testing.T) {
	m, _ := newLimitsController(t, 80, 24)
	m.snapshot.LimitAbsent = false
	m.snapshot.Limit = gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10001,"currency":"USD","interval":"month","enforcement":{"status":"inactive","status":"enforcing"}}`)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Returned enforcement: ambiguous; inspect original") || strings.Contains(view, "Returned enforcement: inactive") {
		t.Fatalf("workspace selected one conflicting enforcement value: %q", view)
	}
}

func TestLimitsWorkspaceSparseAndReadOnlyRates(t *testing.T) {
	const daily = `{"object":"project.rate_limit","id":"rl_daily","model":"daily-model","max_tokens_per_1_day":100,"max_tokens_per_1_minute":null}`
	for _, size := range [][2]int{{40, 12}, {160, 60}} {
		m, backend := newLimitsController(t, size[0], size[1])
		m.snapshot.ProjectID = "proj_demo"
		m.begin("rate_update", gjson.Parse(daily))
		require.Equal(t, "form", m.page)
		require.Equal(t, 6, m.focus, "only the returned numeric field is editable")
		require.Contains(t, ansi.Strip(m.View().Content), "Tokens / day")
		require.NotContains(t, ansi.Strip(m.View().Content), "Requests / minute")
		m.draft.inputs[6].SetValue("9007199254740993")
		limitsControllerKey(m, tea.KeyTab)
		require.Equal(t, 7, m.focus)
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		require.Equal(t, 6, m.focus)
		change, err := m.draft.validate()
		require.NoError(t, err)
		require.Equal(t, map[string]int64{"max_tokens_per_1_day": 9007199254740993}, change.RateValues)
		for _, index := range []int{0, 1} {
			m.draft.inputs[index].SetValue("1")
			_, err := m.draft.validate()
			require.ErrorContains(t, err, "unavailable")
			m.draft.inputs[index].SetValue("")
		}
		for _, model := range []string{"ft:synthetic", "synthetic-shared"} {
			rate := gjson.Parse(strings.Replace(daily, "daily-model", model, 1))
			m.page, m.section, m.selection = "home", 2, 0
			m.snapshot.Rates = []gjson.Result{rate}
			require.Equal(t, "rate_inspect", m.menu()[0].kind)
			require.Contains(t, m.menu()[0].label, "read only")
			limitsControllerKey(m, tea.KeyEnter)
			require.Equal(t, "details", m.page)
			require.Contains(t, m.details, model)
			m.begin("rate_update", rate)
			require.Equal(t, "details", m.page)
		}
		require.Zero(t, backend.writes.Load())
		require.Zero(t, backend.reads.Load())
	}
}

func TestLimitsWorkspaceCompactGeometryAndReviewGate(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {200, 80}} {
		m, backend := newLimitsController(t, size[0], size[1])
		m.snapshot.ProjectID = "proj_demo"
		m.snapshot.Limit = gjson.Parse(limitsTestLimit)
		m.snapshot.Alerts = []gjson.Result{gjson.Parse(limitsControllerAlert)}
		m.snapshot.Rates = []gjson.Result{gjson.Parse(limitsControllerRate)}
		rows, columns := 0, 0
		for section := 0; section < 3; section++ {
			m.section, m.selection = section, 0
			view := m.View()
			require.False(t, view.AltScreen)
			require.NotContains(t, view.Content, "\x1b[1m")
			lines := strings.Split(ansi.Strip(view.Content), "\n")
			if section == 0 {
				rows, columns = len(lines), ansi.StringWidth(lines[1])
			}
			require.Len(t, lines, rows, "tab changes must preserve panel height")
			require.Equal(t, columns, ansi.StringWidth(lines[1]), "tab markers must reserve the same cells")
			require.LessOrEqual(t, len(lines), min(size[1], 18))
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), min(size[0], 76))
			}
		}
		m.begin("limit_set", m.snapshot.Limit)
		m.draft.inputs[0].SetValue("100.01")
		m.review()
		require.Equal(t, "review", m.page)
		require.Contains(t, ansi.Strip(m.View().Content), "Enter: select")
		require.Equal(t, min(size[0], 76)-2, m.viewport.Width())
		require.LessOrEqual(t, m.viewport.Height(), min(size[1], 18)-5)
		require.False(t, m.viewport.AtBottom(), "clamped review must not approve unread content")
		limitsControllerKey(m, tea.KeyTab)
		require.False(t, m.apply)
		m.viewport.GotoBottom()
		limitsControllerKey(m, tea.KeyTab)
		require.True(t, m.apply)
		m.Update(tea.WindowSizeMsg{Width: 180, Height: 70})
		require.False(t, m.apply)
		require.False(t, m.viewport.AtBottom())
		require.Zero(t, backend.writes.Load())
	}
}

func TestLimitsWorkspaceRefreshRetainsUnavailableRateDraftUntilDiscarded(t *testing.T) {
	for _, current := range []string{
		`{"object":"project.rate_limit","id":"rl_synthetic","model":"synthetic-model","max_tokens_per_1_minute":300}`,
		`{"object":"project.rate_limit","id":"rl_synthetic","model":"synthetic-model","max_requests_per_1_minute":null,"max_tokens_per_1_minute":300}`,
	} {
		m, backend := newLimitsController(t, 40, 12)
		m.begin("rate_update", gjson.Parse(limitsControllerRate))
		m.draft.inputs[0].SetValue("101")
		m.draft.inputs[1].SetValue("201")
		m.snapshot.Rates = []gjson.Result{gjson.Parse(current)}
		m.refreshRequired = true
		m.reconcileDraft()
		require.False(t, m.refreshRequired)
		require.True(t, m.draft.hasUnavailableEdits())
		require.Equal(t, "101", m.draft.inputs[0].Value(), "refresh must not silently discard an edit")
		require.Contains(t, m.help(), "Requests / minute: 101")
		require.Equal(t, len(m.draft.inputs), m.focus)
		require.Contains(t, ansi.Strip(m.View().Content), "> Discard unavailable edits")
		require.LessOrEqual(t, len(strings.Split(m.View().Content, "\n")), 12)
		_, err := m.draft.validate()
		require.ErrorContains(t, err, "unavailable")
		limitsControllerKey(m, tea.KeyF1)
		limitsControllerKey(m, tea.KeyEscape)
		limitsControllerKey(m, tea.KeyEnter)
		require.False(t, m.draft.hasUnavailableEdits())
		require.Empty(t, m.draft.inputs[0].Value())
		require.Equal(t, "201", m.draft.inputs[1].Value())
		change, err := m.draft.validate()
		require.NoError(t, err)
		require.Equal(t, map[string]int64{"max_tokens_per_1_minute": 201}, change.RateValues)
		require.Zero(t, backend.writes.Load())
	}
}

func TestLimitsWorkspaceShortDetailsFitContent(t *testing.T) {
	m, _ := newLimitsController(t, 120, 50)
	m.page = "details"
	m.setDetails("One line\nTwo lines")
	require.Equal(t, 2, m.viewport.Height())
	require.True(t, m.viewport.AtBottom())
	require.Len(t, strings.Split(ansi.Strip(m.View().Content), "\n"), 5)
}
