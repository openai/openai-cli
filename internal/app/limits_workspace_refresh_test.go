package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestLimitsWorkspaceAlertRefreshPreservesConcurrentFields(t *testing.T) {
	for _, width := range []int{40, 80} {
		t.Run(fmt.Sprintf("width%d", width), func(t *testing.T) {
			m, backend := newLimitsController(t, width, 24)
			m.begin("alert_update", gjson.Parse(limitsControllerAlert))
			m.draft.inputs[0].SetValue("125.25")
			backend.mutationErr = &limitsMutationFailure{Err: errors.New("synthetic preflight conflict"), Attempted: false}
			m.review()
			m.viewport.GotoBottom()
			limitsControllerKey(m, tea.KeyTab)
			m.Update(limitsControllerRun(t, limitsControllerKey(m, tea.KeyEnter)))
			limitsControllerStarted(t, m, backend)
			require.True(t, m.refreshRequired)
			current := strings.Replace(limitsControllerAlert, "synthetic@example.invalid", "remote@example.test", 1)
			current = strings.Replace(current, `]}`, `],"subject_prefix":"Remote"}`, 1)
			backend.snapshot.Alerts = []gjson.Result{gjson.Parse(current)}
			m.Update(limitsControllerRun(t, limitsControllerKey(m, 'r')))
			require.False(t, m.refreshRequired)
			require.Equal(t, "125.25", m.draft.inputs[0].Value())
			require.Equal(t, "remote@example.test", m.draft.inputs[1].Value())
			require.Equal(t, "Remote", m.draft.inputs[2].Value())
			m.review()
			require.Equal(t, "review", m.page, m.formError)
			require.Equal(t, int32(1), backend.writes.Load(), "refresh and review must not retry")
			backend.mutationErr = nil
			m.viewport.GotoBottom()
			limitsControllerKey(m, tea.KeyTab)
			limitsControllerRun(t, limitsControllerKey(m, tea.KeyEnter))
			change := limitsControllerStarted(t, m, backend)
			require.Equal(t, int64(12525), change.Amount)
			require.Equal(t, []string{"remote@example.test"}, change.Recipients)
			require.Equal(t, "Remote", *change.SubjectPrefix)
			require.Equal(t, current, change.Before.Raw)
		})
	}
}

func TestLimitsWorkspaceAlertRefreshRetainsIntent(t *testing.T) {
	for _, tc := range []struct {
		name, before, edited string
		untouched            bool
	}{
		{"reordered", `["a@example.test","b@example.test"]`, "b@example.test, a@example.test", true},
		{"reordered_duplicates", `["a@example.test","b@example.test","a@example.test"]`, "a@example.test, a@example.test, b@example.test", true},
		{"removed_duplicate", `["a@example.test","a@example.test"]`, "a@example.test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newLimitsController(t, 40, 24)
			before := strings.Replace(limitsControllerAlert, `["synthetic@example.invalid"]`, tc.before, 1)
			m.begin("alert_update", gjson.Parse(before))
			m.draft.inputs[0].SetValue("125.25")
			m.draft.inputs[1].SetValue(tc.edited)
			for _, recipient := range []string{"remote@example.test", "later@example.test"} {
				current := strings.Replace(limitsControllerAlert, "synthetic@example.invalid", recipient, 1)
				m.snapshot.Alerts = []gjson.Result{gjson.Parse(current)}
				m.refreshRequired = true
				m.reconcileDraft()
				want := tc.edited
				if tc.untouched {
					want = recipient
				}
				require.Equal(t, want, m.draft.inputs[1].Value())
				require.Equal(t, recipient, m.draft.initial[1])
				require.Equal(t, "125.25", m.draft.inputs[0].Value())
			}
		})
	}
	t.Run("preserve_prefix_presence_across_refreshes", func(t *testing.T) {
		m, _ := newLimitsController(t, 40, 24)
		m.begin("alert_update", gjson.Parse(limitsControllerAlert))
		m.draft.inputs[0].SetValue("125.25")
		for _, prefix := range []string{`null`, `""`, `"Remote"`, "", `"Later"`} {
			current := limitsControllerAlert
			if prefix != "" {
				current = strings.Replace(current, `]}`, `],"subject_prefix":`+prefix+`}`, 1)
			}
			m.snapshot.Alerts = []gjson.Result{gjson.Parse(current)}
			m.refreshRequired = true
			m.reconcileDraft()
			change, err := m.draft.validate()
			require.NoError(t, err)
			require.Equal(t, prefix != "", change.SubjectPrefixSet)
			if prefix == "" || prefix == "null" {
				require.Nil(t, change.SubjectPrefix)
				require.Empty(t, m.draft.inputs[2].Value())
			} else {
				require.Equal(t, gjson.Parse(prefix).String(), *change.SubjectPrefix)
				require.Equal(t, *change.SubjectPrefix, m.draft.inputs[2].Value())
			}
		}
	})
	for _, edited := range []string{"amount", "recipients", "prefix", "empty_prefix", "null_prefix", "invalid_amount", "invalid_recipients"} {
		t.Run(edited, func(t *testing.T) {
			m, backend := newLimitsController(t, 40, 24)
			m.begin("alert_update", gjson.Parse(limitsControllerAlert))
			m.draft.inputs[0].SetValue("100") // Equivalent dollars remain untouched.
			switch edited {
			case "amount":
				m.draft.inputs[0].SetValue("125.25")
			case "recipients":
				m.draft.inputs[1].SetValue("edited@example.test")
			case "prefix":
				m.draft.inputs[2].SetValue("Edited")
			case "empty_prefix":
				m.draft.prefixMode = 1
			case "null_prefix":
				m.draft.prefixMode = 2
			case "invalid_amount":
				m.draft.inputs[0].SetValue("bad")
			case "invalid_recipients":
				m.draft.inputs[1].SetValue("bad")
			}
			current := strings.Replace(limitsControllerAlert, "10000", "20000", 1)
			current = strings.Replace(current, "synthetic@example.invalid", "remote@example.test", 1)
			current = strings.Replace(current, `]}`, `],"subject_prefix":"Remote"}`, 1)
			m.snapshot.Alerts = []gjson.Result{gjson.Parse(current)}
			m.refreshRequired = true
			m.reconcileDraft()
			want := []string{"200.00", "remote@example.test", "Remote"}
			require.Equal(t, want, m.draft.initial)
			switch edited {
			case "amount":
				want[0] = "125.25"
			case "recipients":
				want[1] = "edited@example.test"
			case "prefix":
				want[2] = "Edited"
			case "empty_prefix", "null_prefix":
				want[2] = ""
			case "invalid_amount":
				want[0] = "bad"
			case "invalid_recipients":
				want[1] = "bad"
			}
			for i, value := range want {
				require.Equal(t, value, m.draft.inputs[i].Value())
			}
			m.review()
			if strings.HasPrefix(edited, "invalid_") {
				require.Equal(t, "form", m.page)
				require.NotEmpty(t, m.formError)
			} else {
				require.Equal(t, "review", m.page, m.formError)
				if edited == "null_prefix" {
					require.True(t, m.change.SubjectPrefixSet)
					require.Nil(t, m.change.SubjectPrefix)
				} else {
					require.Equal(t, want[2], *m.change.SubjectPrefix)
				}
			}
			require.Zero(t, backend.writes.Load())
		})
	}
	t.Run("return_to_original_prefix_after_refresh", func(t *testing.T) {
		m, _ := newLimitsController(t, 40, 24)
		before := strings.Replace(limitsControllerAlert, `]}`, `],"subject_prefix":"A"}`, 1)
		m.begin("alert_update", gjson.Parse(before))
		m.draft.inputs[2].SetValue("B")
		current := strings.Replace(before, `"subject_prefix":"A"`, `"subject_prefix":"C"`, 1)
		m.snapshot.Alerts = []gjson.Result{gjson.Parse(current)}
		m.refreshRequired = true
		m.reconcileDraft()
		require.Equal(t, "B", m.draft.inputs[2].Value())
		m.draft.inputs[2].SetValue("A")
		m.review()
		require.Equal(t, "review", m.page, m.formError)
		require.Equal(t, "A", *m.change.SubjectPrefix)
	})
}
