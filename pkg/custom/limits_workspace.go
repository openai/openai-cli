package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/clihelp"
	clioutput "github.com/openai/openai-cli/internal/output"
	clirequest "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type limitsWorkResult struct {
	snapshot  limitsSnapshot
	value     gjson.Result
	err       error
	mutation  bool
	operation string
}
type limitsMenuItem struct {
	label, kind string
	value       gjson.Result
}

type limitsWorkspace struct {
	detailsHeading                                string
	previewExecutable                             string
	invocation                                    string
	lastSavedOperation, workerSavedOperation      string
	mutationError                                 error
	workerSaved                                   gjson.Result
	ctx                                           context.Context
	cancel                                        context.CancelFunc
	backend                                       limitsBackend
	output                                        *limitsWorkspaceOutput
	snapshot                                      limitsSnapshot
	width, height, section, selection, focus      int
	page, note, formError                         string
	details                                       string
	busy, quitting, interrupted, locked, apply    bool
	refreshRequired, refreshingDraft, color, dark bool
	lastSaved                                     gjson.Result
	draft                                         limitsDraft
	change                                        limitsChange
	viewport                                      viewport.Model
	requestArgs                                   []string
	omitPreview                                   bool
	shell                                         string
	work                                          sync.WaitGroup
	mu                                            sync.Mutex
	workerSnapshot                                *limitsSnapshot
	workerError                                   error
	mutationPending                               bool
}

type limitsInterrupt struct{ error }

func (*limitsInterrupt) ExitCode() int   { return 130 }
func (e *limitsInterrupt) Unwrap() error { return e.error }

// limitsWorkspaceOutput records failures that the terminal renderer may ignore.
// Its output context stays active so shutdown can still restore terminal modes.
type limitsWorkspaceOutput struct {
	clioutput.TerminalWriter
	stop context.CancelFunc
	mu   sync.Mutex
	err  error
}

var _ term.File = (*limitsWorkspaceOutput)(nil)

func (w *limitsWorkspaceOutput) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err != nil {
		w.mu.Lock()
		if w.err == nil {
			w.err = err
		}
		w.mu.Unlock()
		w.stop()
	}
	return n, err
}

func (w *limitsWorkspaceOutput) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *limitsWorkspaceOutput) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func runLimitsWorkspace(ctx context.Context, cmd *cli.Command, backend limitsBackend, opts ShowJSONOpts) error {
	file := opts.Stdout.(*os.File)
	width, height, err := term.GetSize(file.Fd())
	if err != nil {
		return err
	}
	programContext, stop := context.WithCancel(ctx)
	defer stop()
	m := &limitsWorkspace{ctx: programContext, cancel: stop, backend: backend, width: width, height: height, page: "home",
		color: os.Getenv("NO_COLOR") == "", dark: true,
		viewport: viewport.New(viewport.WithWidth(max(1, width)), viewport.WithHeight(max(1, height-5)))}
	m.viewport.SoftWrap, m.viewport.FillHeight = false, false
	m.requestArgs, m.omitPreview = clirequest.NextCommandOptions(cmd.Root())
	m.shell = terminal.ParentShell(ctx)
	m.invocation = clihelp.DisplayInvocation(cmd.Root())
	m.previewExecutable, err = os.Executable()
	if err != nil || strings.HasPrefix(m.invocation, "go run ") {
		m.omitPreview = true
	}
	m.output = &limitsWorkspaceOutput{TerminalWriter: clioutput.TerminalWriter{
		Writer: clioutput.Writer{Context: context.Background(), Sink: opts.Stdout}, File: file}, stop: stop}
	options := []tea.ProgramOption{tea.WithInput(cmd.Root().Reader), tea.WithOutput(m.output), tea.WithContext(programContext), tea.WithWindowSize(width, height)}
	if !m.color {
		options = append(options, tea.WithColorProfile(colorprofile.NoTTY))
	}
	program := tea.NewProgram(m, options...)
	_, runErr := program.Run()
	stop()
	m.work.Wait()
	return finishLimitsWorkspace(ctx, m, runErr, opts)
}

// Finalization retains independent operation, cancellation, and output failures.
// A broken output pipe must not hide an uncertain mutation or failed source.
func finishLimitsWorkspace(ctx context.Context, m *limitsWorkspace, runErr error, opts ShowJSONOpts) error {
	m.mu.Lock()
	workerErr, pending, mutationErr := m.workerError, m.mutationPending, m.mutationError
	if m.workerSnapshot != nil {
		m.snapshot = *m.workerSnapshot
	}
	if m.workerSaved.Exists() {
		m.lastSaved, m.lastSavedOperation = m.workerSaved, m.workerSavedOperation
	}
	m.mu.Unlock()
	var causes []error
	removedLimitErr := m.removedLimitFailure()
	ignored := []error{removedLimitErr}
	if ctx.Err() == nil && !m.interrupted {
		ignored = append(ignored, context.Canceled)
	}
	sourcesIncomplete := false
	for _, failure := range m.snapshot.Errors {
		if !limitsOnlyErrors(failure.Err, ignored...) {
			sourcesIncomplete = true
			causes = append(causes, failure.Err)
		}
	}
	// Load returns an aggregate of the recorded source errors. Keep each source
	// once, and do not reintroduce cancellation caused by normal workspace exit.
	accounted := append([]error{}, ignored...)
	for _, failure := range m.snapshot.Errors {
		accounted = append(accounted, failure.Err)
	}
	if limitsOnlyErrors(workerErr, accounted...) {
		workerErr = nil
	}
	// Closing the workspace cancels its worker. That internal cancellation is
	// not a failed read. Preserve the caller's cancellation separately below.
	if workerErr != nil {
		causes = append(causes, workerErr)
	}
	if m.refreshRequired || pending || m.locked {
		causes = append(causes, mutationErr)
	}
	incomplete := m.refreshRequired || sourcesIncomplete || workerErr != nil
	message, humanDetail := "", ""
	if pending || m.locked {
		message = "A limits change may have succeeded. Check its current state before repeating it.\n" + m.readRecovery()
		if mutationErr != nil {
			humanDetail = limitsMutationHint(mutationErr, m.invocation)
		}
	} else if m.refreshRequired {
		message = limitsPreflightHint(mutationErr, m.invocation)
		if m.lastSaved.Exists() {
			message += "\nA preceding change was confirmed. The latest change was not sent."
		}
	} else if incomplete {
		message = "The limits overview is incomplete. Refresh before another change."
		if m.lastSaved.Exists() {
			message = "A change was confirmed, but the overview refresh failed. Inspect the resource before another change."
		}
	}
	var outputErr error
	if m.output != nil {
		outputErr = m.output.Err()
	}
	interrupted := m.interrupted || errors.Is(runErr, tea.ErrInterrupted)
	if ctx.Err() != nil {
		causes = append(causes, ctx.Err())
		if message == "" {
			message = "The limits workspace was canceled or timed out."
			if m.lastSaved.Exists() {
				message += " A preceding change was confirmed. Inspect the resource before another change."
			}
		}
	}
	if interrupted {
		causes = append(causes, context.Canceled)
		if message == "" {
			message = "Limits workspace interrupted."
			if m.lastSaved.Exists() {
				message += " A preceding change was confirmed. Inspect the resource before another change."
			}
		}
	}
	// The output guard stops Tea after a sink failure. Preserve independent
	// program failures, without treating that expected stop as another failure.
	programErr := runErr
	var expectedStops []error
	if m.quitting || interrupted || outputErr != nil || ctx.Err() != nil {
		expectedStops = append(expectedStops, tea.ErrProgramKilled, context.Canceled)
	}
	if interrupted {
		expectedStops = append(expectedStops, tea.ErrInterrupted)
	}
	if limitsOnlyErrors(programErr, expectedStops...) {
		programErr = nil
	}
	if programErr != nil {
		causes = append(causes, programErr)
	}
	operationErr := errors.Join(causes...)
	if message == "" && operationErr != nil {
		message = "The limits workspace could not complete. Inspect the current settings before another change."
	}
	if outputErr != nil || interrupted || ctx.Err() != nil || pending || m.locked || programErr != nil {
		if message != "" {
			operationErr = &limitsWorkflowFailure{message: message, humanDetail: humanDetail, cause: operationErr}
		}
		failure := errors.Join(operationErr, outputErr)
		if interrupted {
			return &limitsInterrupt{failure}
		}
		return failure
	}
	var receiptErr error
	if m.lastSaved.Exists() {
		opts.Operation, opts.OutputKind = m.lastSavedOperation, OutputResponse
		opts.Title = "Last confirmed result"
		receiptErr = ShowJSON(m.lastSaved, opts)
	}
	if incomplete {
		return errors.Join(&limitsWorkflowFailure{message: message, cause: operationErr}, receiptErr)
	}
	if receiptErr != nil {
		return receiptErr
	}
	if removedLimitErr != nil && resolvedOutputFormat(opts) == "text" {
		_, err := (clioutput.Writer{Context: ctx, Sink: opts.Stdout}).WriteString("Removal confirmed. Current hard-limit configuration was not returned (HTTP 404).\n")
		return err
	}
	if m.lastSaved.Exists() {
		return nil
	}
	_, err := (clioutput.Writer{Context: ctx, Sink: opts.Stdout}).WriteString("Limits workspace closed.\n")
	return err
}

func (m *limitsWorkspace) readRecovery() string {
	invocation := m.invocation
	if invocation == "" {
		invocation = "openai"
	}
	resource := "admin organization "
	if m.snapshot.ProjectID != "" {
		resource += "projects "
	}
	switch m.change.Kind {
	case "alert_create", "alert_update", "alert_delete":
		resource += "spend-alerts list"
	case "rate_update":
		resource += "rate-limits list-rate-limits"
	default:
		resource += "spend-limit retrieve"
	}
	return "Read options: " + invocation + " help " + resource + "\nKeep the original connection, authentication, and scope options."
}

func (m *limitsWorkspace) Init() tea.Cmd { return tea.Batch(m.load(), tea.RequestBackgroundColor) }

func (m *limitsWorkspace) launch(change *limitsChange) tea.Cmd {
	m.busy = true
	result := make(chan limitsWorkResult, 1)
	operation := ""
	if change != nil {
		operation = limitsChangeOperation(change.Kind, m.snapshot.ProjectID != "")
	}
	m.work.Add(1)
	if change != nil {
		m.mu.Lock()
		m.mutationPending = true
		m.mu.Unlock()
	}
	go func() {
		defer m.work.Done()
		var r limitsWorkResult
		if change == nil {
			r.snapshot, r.err = m.backend.Load(m.ctx)
		} else {
			r.mutation, r.operation = true, operation
			r.value, r.err = m.backend.Mutate(m.ctx, *change)
		}
		m.mu.Lock()
		m.workerError = r.err
		if !r.mutation {
			m.workerSnapshot = &r.snapshot
		}
		if r.mutation && r.err == nil {
			m.workerSaved, m.workerSavedOperation = r.value, r.operation
		}
		if r.mutation {
			m.mutationError = r.err
		}
		var rejected *limitsMutationFailure
		if r.mutation && (r.err == nil || errors.As(r.err, &rejected) && !rejected.Attempted) {
			m.mutationPending = false
		}
		m.mu.Unlock()
		result <- r
	}()
	return func() tea.Msg { return <-result }
}

func (m *limitsWorkspace) load() tea.Cmd { return m.launch(nil) }
func (m *limitsWorkspace) small() bool   { return m.width < 40 || m.height < 12 }

func (m *limitsWorkspace) reconcileDraft() {
	change := m.draft.change
	var current gjson.Result
	switch change.Kind {
	case "limit_set", "limit_delete":
		current = m.snapshot.Limit
		if change.Kind == "limit_set" && (m.snapshot.LimitAbsent || m.snapshot.LimitUnconfirmed) && m.limitCanBeSet() {
			change.Before, change.BeforeMissing = gjson.Result{}, true
			m.draft.change, m.refreshRequired, m.formError = change, false, "Baseline refreshed. Review the before and after values."
			return
		}
	case "alert_create":
		m.refreshRequired, m.formError = false, "Review the draft before creating the alert."
		return
	case "alert_update", "alert_delete":
		for _, record := range m.snapshot.Alerts {
			if record.Get("id").String() == change.ID {
				if current.Exists() {
					m.formError = "Duplicate alert IDs. Inspect with the API command."
					return
				}
				current = record
			}
		}
	case "rate_update":
		for _, record := range m.snapshot.Rates {
			if record.Get("id").String() == change.ID {
				if current.Exists() {
					m.formError = "Duplicate rate IDs. Inspect with the API command."
					return
				}
				current = record
			}
		}
	}
	if !current.Exists() || limitsEditableBaseline(current, change.Kind) != nil {
		m.formError = "Cannot refresh this draft. Inspect the resource with the API command."
		return
	}
	change.Before, change.BeforeMissing = current, false
	m.draft.change, m.refreshRequired, m.formError = change, false, "Baseline refreshed. Review the before and after values."
}

func (m *limitsWorkspace) menu() []limitsMenuItem {
	var rows []limitsMenuItem
	switch m.section {
	case 0:
		if m.limitCanBeSet() {
			rows = append(rows, limitsMenuItem{label: "Set hard spend limit", kind: "limit_set", value: m.snapshot.Limit})
			if m.snapshot.Limit.Exists() {
				rows = append(rows, limitsMenuItem{label: "Remove hard spend limit", kind: "limit_delete", value: m.snapshot.Limit})
			}
		}
	case 1:
		rows = append(rows, limitsMenuItem{label: "Create spend alert", kind: "alert_create"})
		for _, alert := range m.snapshot.Alerts {
			rows = append(rows, limitsMenuItem{label: "Edit " + alert.Get("id").String() + "  " + limitsThresholdLabel(alert), kind: "alert_update", value: alert})
		}
	case 2:
		for _, rate := range m.snapshot.Rates {
			rows = append(rows, limitsMenuItem{label: rate.Get("model").String() + "  " + rate.Get("id").String(), kind: "rate_update", value: rate})
		}
	}
	return rows
}

func (m *limitsWorkspace) begin(kind string, value gjson.Result) {
	if m.locked {
		m.note = "Writes are locked after a failed save. Inspect the resource before reopening."
		return
	}
	for _, failure := range m.snapshot.Errors {
		if failure.Resource == "project" {
			m.note = "Project scope unavailable. Refresh: r"
			return
		}
	}
	change := limitsChange{Kind: kind, ID: value.Get("id").String(), Before: value, BeforeMissing: kind == "limit_set" && (m.snapshot.LimitAbsent || m.snapshot.LimitUnconfirmed)}
	if kind == "limit_set" || kind == "limit_delete" {
		change.ID = ""
	}
	draft, err := newLimitsDraft(change)
	if err != nil {
		m.note = "This record cannot be edited safely. Use i to inspect the original fields."
		return
	}
	m.draft, m.change, m.focus, m.formError = draft, change, 0, ""
	if len(draft.inputs) == 0 {
		m.review()
		return
	}
	m.page = "form"
	m.setFocus(0)
}

func (m *limitsWorkspace) setFocus(next int) {
	for i := range m.draft.inputs {
		m.draft.inputs[i].Blur()
	}
	count := len(m.draft.inputs) + 1
	if len(m.draft.inputs) == 3 {
		count++
	}
	m.focus = (next + count) % count
	if m.focus < len(m.draft.inputs) {
		m.draft.inputs[m.focus].Focus()
	}
}

func (m *limitsWorkspace) review() {
	if m.refreshRequired {
		m.formError = "Refresh with r before reviewing this draft."
		return
	}
	change, err := m.draft.validate()
	if err != nil {
		m.formError = err.Error()
		return
	}
	m.change, m.page, m.apply, m.formError = change, "review", false, ""
	m.setDetails(limitsReview(change, m.snapshot.ProjectID, m.requestArgs, m.omitPreview, m.shell, m.previewExecutable))
}

func (m *limitsWorkspace) setDetails(content string) {
	m.details = content
	m.viewport.SetWidth(max(1, m.width-2))
	m.viewport.SetHeight(max(1, m.height-5))
	m.viewport.SetContent(ansi.Hardwrap(content, max(1, m.width-2), true))
	m.viewport.GotoTop()
}

func (m *limitsWorkspace) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, tea.Quit
	}
	switch msg := message.(type) {
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.apply = false
		if m.page == "review" {
			m.setDetails(limitsReview(m.change, m.snapshot.ProjectID, m.requestArgs, m.omitPreview, m.shell, m.previewExecutable))
		}
		if m.page == "details" {
			m.setDetails(m.details)
		}
		m.viewport.SetWidth(max(1, msg.Width-2))
		m.viewport.SetHeight(max(1, msg.Height-5))
		return m, nil
	case limitsWorkResult:
		m.busy = false
		if msg.mutation {
			if msg.err != nil {
				var rejected *limitsMutationFailure
				m.locked = !(errors.As(msg.err, &rejected) && !rejected.Attempted)
				m.refreshRequired, m.apply, m.page = !m.locked, false, "form"
				m.formError = "Save not confirmed. Press ? for recovery. Exit before another change."
				if !m.locked {
					m.formError = limitsPreflightHint(rejected.Err, m.invocation)
				}
				return m, nil
			}
			m.lastSaved, m.lastSavedOperation = msg.value, msg.operation
			m.page, m.note, m.selection = "home", "Saved. Refreshing the overview.", 0
			return m, m.load()
		}
		m.snapshot = msg.snapshot
		m.note = "Website controls and source errors: ?"
		if m.lastSaved.Exists() {
			m.note = "Change confirmed. Last result prints when you exit."
		}
		if msg.err != nil || len(msg.snapshot.Errors) != 0 {
			m.note = "Some sources failed. Press ? for recovery; r to refresh."
			if expected := m.removedLimitFailure(); expected != nil && len(msg.snapshot.Errors) == 1 && limitsOnlyErrors(msg.err, expected) {
				m.note = "Removal confirmed. Current limit not returned."
			}
		}
		if m.refreshingDraft {
			m.refreshingDraft = false
			m.reconcileDraft()
		}
		m.selection = min(m.selection, max(0, len(m.menu())-1))
		return m, nil
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" || (m.page == "home" && (key == "q" || key == "esc")) {
			m.interrupted, m.quitting = key == "ctrl+c", true
			m.cancel()
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		if key == "esc" {
			m.page, m.apply = "home", false
			return m, nil
		}
		if m.small() {
			return m, nil
		}
		if key == "f1" || key == "?" && m.page != "form" || key == "?" && m.locked {
			m.page, m.detailsHeading = "details", "Limits workspace help"
			m.setDetails(m.help())
			return m, nil
		}
		switch m.page {
		case "home":
			switch key {

			case "r":
				m.note = "Refreshing…"
				return m, m.load()
			case "left", "right", "tab", "shift+tab":
				count, delta := 2, 1
				if m.snapshot.ProjectID != "" {
					count = 3
				}
				if key == "left" || key == "shift+tab" {
					delta = -1
				}
				m.section, m.selection = (m.section+count+delta)%count, 0
			case "up":
				m.selection = max(0, m.selection-1)
			case "down":
				m.selection = min(max(0, len(m.menu())-1), m.selection+1)
			case "enter", "d", "i":
				rows := m.menu()
				if len(rows) == 0 {
					return m, nil
				}
				row := rows[m.selection]
				if key == "i" {
					if row.value.Exists() {
						m.page, m.detailsHeading = "details", "Original API record"
						m.setDetails(limitsSafeJSON(row.value))
					}
				} else if key == "d" {
					if row.kind == "alert_update" {
						m.begin("alert_delete", row.value)
					}
				} else {
					m.begin(row.kind, row.value)
				}
			}
		case "form":
			if key == "r" && m.refreshRequired {
				m.refreshingDraft = true
				return m, m.load()
			}
			switch key {
			case "tab":
				m.setFocus(m.focus + 1)
				return m, nil
			case "shift+tab":
				m.setFocus(m.focus - 1)
				return m, nil
			case "enter":
				if m.focus == len(m.draft.inputs) && len(m.draft.inputs) == 3 {
					m.draft.prefixMode = (m.draft.prefixMode + 1) % 3
				} else if m.focus >= len(m.draft.inputs) {
					if !m.locked {
						m.review()
					}
				} else {
					m.setFocus(m.focus + 1)
				}
				return m, nil
			}
		case "review":
			if key == "tab" || key == "shift+tab" {
				if m.viewport.AtBottom() {
					m.apply = !m.apply
				}
				return m, nil
			}
			if key == "enter" {
				if !m.apply {
					m.page = "form"
					m.setFocus(0)
					return m, nil
				}
				if m.viewport.AtBottom() && !m.locked && m.ctx.Err() == nil && (m.output == nil || m.output.Err() == nil) {
					m.apply = false
					change := m.change
					return m, m.launch(&change)
				}
				return m, nil
			}
		}
	}
	if m.page == "review" || m.page == "details" {
		var command tea.Cmd
		m.viewport, command = m.viewport.Update(message)
		if !m.viewport.AtBottom() {
			m.apply = false
		}
		return m, command
	}
	if m.page == "form" && m.focus < len(m.draft.inputs) && !m.small() {
		var command tea.Cmd
		m.draft.inputs[m.focus], command = m.draft.inputs[m.focus].Update(message)
		return m, command
	}
	return m, nil
}

func limitsThresholdLabel(value gjson.Result) string {
	amount, err := limitsInteger(value.Get("threshold_amount"))
	if err == nil && limitsUniqueJSON(value) == nil && value.Get("currency").String() == "USD" && value.Get("interval").String() == "month" {
		return "USD " + formatLimitsDollars(amount) + " / month"
	}
	return "Inspect raw units"
}

func (m *limitsWorkspace) heading() string {
	if m.page == "form" || m.page == "review" {
		return limitsChangeTitle(m.change.Kind)
	}
	if m.page == "details" {
		if m.detailsHeading != "" {
			return m.detailsHeading
		}
		return "Original API record"
	}
	return "Limits"
}

func (m *limitsWorkspace) scope() string {
	if m.snapshot.ProjectID != "" {
		return "Project " + m.snapshot.ProjectID
	}
	return "Organization"
}

func (m *limitsWorkspace) sourceSummary() string {
	switch m.section {
	case 0:
		if m.snapshot.LimitUnconfirmed {
			if m.removedLimitFailure() != nil {
				return "Removal confirmed. Current limit unknown."
			}
			return "Hard limit unknown. Review before replacing."
		}
		if !m.limitReadable() {
			return "Hard spend limit unavailable. Refresh: r"
		}
		if m.snapshot.LimitAbsent {
			return "No hard spend limit configured."
		}
		if m.snapshot.Limit.Exists() {
			return "Hard limit: " + limitsThresholdLabel(m.snapshot.Limit)
		}
		return "Hard spend limit unavailable. Refresh to retry."
	case 1:
		return fmt.Sprintf("%d alerts loaded. Complete: %t", len(m.snapshot.Alerts), m.snapshot.AlertsComplete)
	default:
		return fmt.Sprintf("%d rate records loaded. Complete: %t", len(m.snapshot.Rates), m.snapshot.RatesComplete)
	}
}

func (m *limitsWorkspace) limitReadable() bool {
	for _, failure := range m.snapshot.Errors {
		if failure.Resource == "spend_limit" || failure.Resource == "project" {
			return false
		}
	}
	return true
}

func limitsSafeJSON(value gjson.Result) string { return limitsSafeLine(value.Raw) }

func limitsPreflightHint(err error, invocation ...string) string {
	bin := "openai"
	if len(invocation) != 0 && invocation[0] != "" {
		bin = invocation[0]
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return "Admin authentication failed. No write sent.\nKey setup: " + bin + " help setup admin"
		case 403:
			return "The API denied access. No write sent.\nCheck Admin-key permissions and organization or project access.\nAccess setup: " + bin + " help setup admin"
		case 404:
			return "The requested resource was not returned. No write sent.\nCheck the API address, scope, and resource ID. Press r to refresh."
		case 429:
			switch apiErr.Code {
			case "organization_spend_limit_exceeded":
				return "The API reports an organization spend limit was reached. No write sent.\nLimit options: " + bin + " help admin organization spend-limit retrieve"
			case "project_spend_limit_exceeded":
				return "The API reports a project spend limit was reached. No write sent.\nLimit options: " + bin + " help admin organization projects spend-limit retrieve"
			case "insufficient_quota", "billing_hard_limit_reached", "credit_balance_exhausted", "organization_usage_limit_exceeded":
				return "The API reports a billing or usage limit. No write sent.\nCheck billing and approved usage: https://platform.openai.com/settings/organization/limits"
			default:
				return "The API returned HTTP 429. No write sent.\nWait before refreshing. Check API error details if this continues."
			}
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "The check was interrupted. No write sent. Press r to refresh before trying again."
	}
	if err == nil {
		return "No change sent. Press r to refresh this draft's baseline."
	}
	// Only exact locally defined messages select specific recovery guidance.
	switch err.Error() {
	case "Rate-limit editing requires a complete list. Reopen with a larger --max-items value or -1.":
		return "No write sent. Reopen with --max-items -1 to check every rate record."
	case "The configuration changed. Refresh before applying this change.":
		return "Record changed. No write sent. Press r to refresh and review the new baseline."
	default:
		return "No change sent. Press r to refresh this draft's baseline."
	}
}

func (m *limitsWorkspace) limitCanBeSet() bool {
	for _, failure := range m.snapshot.Errors {
		if failure.Resource == "project" || failure.Resource == "spend_limit" && !m.snapshot.LimitUnconfirmed {
			return false
		}
	}
	return m.snapshot.Limit.Exists() || m.snapshot.LimitAbsent || m.snapshot.LimitUnconfirmed
}

func (m *limitsWorkspace) costsSummary() string {
	if m.snapshot.CostsTotal == "" {
		return "Current-month Costs unavailable. ?: details"
	}
	label := "Reported Costs"
	if !m.snapshot.CostsComplete {
		label += " (incomplete)"
	}
	return label + ": USD " + m.snapshot.CostsTotal
}

func (m *limitsWorkspace) help() string {
	invocation := m.invocation
	if invocation == "" {
		invocation = "openai"
	}
	text := "Spend manages hard monthly USD limits.\nAlerts notify recipients; they do not enforce caps.\nRates manages six project rate fields.\n\nEvery change requires a before/after review.\nRead to the end, then Tab to Apply.\nBack remains the default action.\n\nUse r to refresh. Use i to inspect original fields.\nUse --max-items -1 to load complete lists.\n\n"
	if m.locked {
		m.mu.Lock()
		failure := m.mutationError
		m.mu.Unlock()
		text = "The latest save was not confirmed. It may have succeeded.\nWrites remain locked, including after refresh.\n" + m.readRecovery() + "\n" + limitsMutationHint(failure, invocation) + "\n\n" + text
	} else if m.refreshRequired {
		m.mu.Lock()
		failure := m.mutationError
		m.mu.Unlock()
		text = limitsPreflightHint(failure, invocation) + "\n\n" + text
	}
	for _, failure := range m.snapshot.Errors {
		// Source names come from our loader, never response text.
		text += "Source unavailable: " + failure.Resource + "\n" + limitsReadHint(failure.Err, invocation) + "\n\n"
	}
	if len(m.snapshot.Errors) != 0 {
		route := "admin limits show"
		if m.snapshot.ProjectID != "" {
			route = "projects limits show"
		}
		text += "Overview options: " + invocation + " help " + route + "\nKeep the original connection, authentication, and scope options.\nUse --format json to inspect available records and source_errors.\n\n"
	}
	if m.snapshot.LimitUnconfirmed {
		text += "The current hard limit could not be confirmed.\nSet hard spend limit requires review and can create or replace a limit.\n\n"
	}
	if m.snapshot.CostsStart != 0 && m.snapshot.CostsEnd != 0 {
		text += m.costsSummary() + "\nQueried period: " + time.Unix(m.snapshot.CostsStart, 0).UTC().Format(time.RFC3339) + " to " + time.Unix(m.snapshot.CostsEnd, 0).UTC().Format(time.RFC3339) + " (exclusive).\nToday's Costs remain partial.\nCosts can lag and do not determine enforcement state.\n\n"
	}
	text += "Soft budgets, independent enforcement settings, account tiers, credits, and organization rate catalogs need the website:\nhttps://platform.openai.com/settings/organization/limits\n\nThe public API exposes absolute-value alerts, not website percentage alerts or triggered status.\nRemoving a hard limit stops that cap; inspect soft budgets on the website.\n\nHistorical Costs command:\n" + invocation + " help admin usage costs\n\nThe workspace checks current state before saving. This check is not atomic.\nThe workspace sends each confirmed mutation once, without automatic retries.\nDirect API commands omit these safeguards and can retry.\n"
	return text
}

func limitsReadHint(err error, invocation string) string {
	// Read failures have no pending mutation. Reuse the classification without
	// presenting a failed read as a failed save.
	var summary *limitsCostSummaryError
	if errors.As(err, &summary) {
		return summary.Error() + "\nCosts options: " + invocation + " help admin usage costs"
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403, 404, 429:
			return strings.ReplaceAll(limitsPreflightHint(err, invocation), "No write sent.", "No change was attempted.")
		case 400, 422:
			return fmt.Sprintf("The API returned HTTP %d. No change was attempted.\nRecheck the request fields and scope, then refresh.", apiErr.StatusCode)
		default:
			return fmt.Sprintf("The API returned HTTP %d. No change was attempted.\nPress r to refresh. If this continues, check API availability and access.", apiErr.StatusCode)
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "The read was interrupted. Press r to refresh."
	}
	return "The source could not be read. Check the API address and access, then refresh."
}

func limitsMutationHint(err error, invocation string) string {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403, 404, 429:
			return strings.ReplaceAll(limitsPreflightHint(err, invocation), "No write sent.", "The change is not confirmed.")
		case 400, 422:
			return fmt.Sprintf("The API returned HTTP %d. The change is not confirmed.\nRecheck the reviewed fields. Read the current record before another change.", apiErr.StatusCode)
		default:
			return fmt.Sprintf("The API returned HTTP %d. The change is not confirmed.\nRead the current record before another change.", apiErr.StatusCode)
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "The response was interrupted. The change is not confirmed.\nRead the current record before another change."
	}
	if err != nil {
		switch err.Error() {
		case "The API returned different values from the reviewed change. Inspect the returned record before another change.":
			return "The API returned different values from the reviewed change. Read the current record before another change."
		case "The API returned an incomplete confirmation. Refresh and inspect the resource before another change.":
			return "The API response did not confirm the change. Read the current record before another change."
		}
	}
	return "The response did not confirm the change. Check the API address and connection before reading its current state."
}

func (m *limitsWorkspace) removedLimitFailure() error {
	if m.lastSavedOperation != limitsChangeOperation("limit_delete", m.snapshot.ProjectID != "") || m.lastSaved.Get("deleted").Type != gjson.True || !m.snapshot.LimitUnconfirmed {
		return nil
	}
	for _, failure := range m.snapshot.Errors {
		if failure.Resource == "spend_limit" && limitsHTTPStatus(failure.Err, 404) {
			return failure.Err
		}
	}
	return nil
}

// Inspect each branch; errors.Is alone also matches joins with unrelated errors.
func limitsOnlyErrors(err error, expected ...error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !limitsOnlyErrors(child, expected...) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return limitsOnlyErrors(wrapped.Unwrap(), expected...)
	}
	for _, target := range expected {
		if target != nil && errors.Is(err, target) {
			return true
		}
	}
	return false
}
