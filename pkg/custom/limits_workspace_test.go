package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-go/v3"
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
	if !m.locked || m.apply || m.page != "form" || m.draft.inputs[0].Value() != "100.01" || !strings.Contains(m.formError, "inspect") {
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
	draft.inputs[4].SetValue("0") // Explicit zero remains a real change.
	change, err := draft.validate()
	want := map[string]int64{"max_tokens_per_1_minute": 201, "max_audio_megabytes_per_1_minute": 0}
	if err != nil || !reflect.DeepEqual(change.RateValues, want) {
		t.Fatalf("rate changes=%v error=%v; want %v", change.RateValues, err, want)
	}
	for _, tc := range []struct {
		text  string
		value int64
	}{{"-1", -1}, {"-9223372036854775808", math.MinInt64}} {
		draft.inputs[1].SetValue(tc.text)
		change, err := draft.validate()
		if err != nil || change.RateValues["max_tokens_per_1_minute"] != tc.value {
			t.Errorf("signed rate %q was not preserved: %v, %v", tc.text, change.RateValues, err)
		}
	}
	for _, invalid := range []string{"-", "1.5", "1e3", "9223372036854775808", "-9223372036854775809"} {
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
				}
			}
			m.review()
			if m.page != "review" {
				t.Fatalf("fixture could not enter review: %s", m.formError)
			}
			m.viewport.GotoBottom()
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "> Back") || !strings.Contains(view, "Apply change") || !strings.Contains(view, "Ctrl+C") {
				t.Errorf("minimum review hides confirmation: %q", view)
			}
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
			for _, want := range []string{"Direct API command", "skips the workspace preflight", "can retry automatically", "creates or replaces"} {
				if !strings.Contains(preview, want) {
					t.Errorf("preview omitted %q", want)
				}
			}
			if strings.Contains(preview, "Equivalent command") || strings.Contains(preview, "Not configured") {
				t.Fatal("preview claimed unsupported equivalence or confirmed absence")
			}
			command := strings.SplitN(strings.SplitN(preview, "Direct API command\n", 2)[1], "\n", 2)[0]
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
				m.output = &listNavigationOutput{err: writeErr}
			case "read and terminal output":
				m.workerError = original
				m.output = &listNavigationOutput{err: writeErr}
			case "mutation and terminal output":
				m.mutationPending = true
				m.mutationError = original
				m.output = &listNavigationOutput{err: writeErr}
			case "cancellation and terminal output":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				original = context.Canceled
				m.interrupted = true
				m.output = &listNavigationOutput{err: writeErr}
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

func TestLimitsWorkspaceReceiptUsesMutationProjection(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			m, _ := newLimitsController(t, 80, 24)
			m.quitting = true
			m.workerSaved = gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10001,"currency":"USD","interval":"month","enforcement":{"status":"inactive"}}`)
			m.workerSavedOperation = limitsChangeOperation("limit_set", false)
			var out bytes.Buffer
			err := finishLimitsWorkspace(t.Context(), m, nil, ShowJSONOpts{Context: t.Context(), Format: format, Stdout: &out, Stderr: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			if format == "text" {
				if !strings.Contains(out.String(), "USD 100.01") || !strings.Contains(out.String(), "inactive") {
					t.Fatalf("receipt lost threshold units or returned state: %q", out.String())
				}
			} else if !strings.Contains(out.String(), `"threshold_amount": 10001`) || strings.Contains(out.String(), "spend_threshold") {
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
	if !strings.Contains(view, "Costs: USD 123.000000000000000001 (incomplete)") || !strings.Contains(view, "Returned enforcement: inactive") {
		t.Fatalf("workspace conflated Costs and returned enforcement: %q", view)
	}
	help := m.help()
	if !strings.Contains(help, "Queried period:") || !strings.Contains(help, "Costs can lag and do not determine enforcement state") {
		t.Fatal("Costs view omitted its coverage and enforcement limitations")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("minimum-size overview exceeded its width: %q", line)
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
