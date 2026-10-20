package custom

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
)

var limitsRateInputs = []struct{ key, label string }{
	{"max_requests_per_1_minute", "Requests / minute"},
	{"max_tokens_per_1_minute", "Tokens / minute"},
	{"max_requests_per_1_day", "Requests / day"},
	{"max_images_per_1_minute", "Images / minute"},
	{"max_audio_megabytes_per_1_minute", "Audio megabytes / minute"},
	{"batch_1_day_max_input_tokens", "Batch input tokens / day"},
}

type limitsDraft struct {
	change     limitsChange
	labels     []string
	inputs     []textinput.Model
	initial    []string
	prefixMode int // Preserve, replace, or null. Empty replacement stays an empty string.
}

func newLimitsDraft(change limitsChange) (limitsDraft, error) {
	d := limitsDraft{change: change}
	if change.Before.Exists() {
		if err := limitsEditableBaseline(change.Before, change.Kind); err != nil {
			return d, err
		}
	}
	add := func(label, value string) {
		input := textinput.New()
		input.Prompt = ""
		input.CharLimit = 0
		input.SetValue(value)
		d.labels = append(d.labels, label)
		d.inputs = append(d.inputs, input)
		d.initial = append(d.initial, value)
	}
	switch change.Kind {
	case "limit_set", "alert_create", "alert_update":
		amount := ""
		if change.Before.Get("threshold_amount").Exists() {
			amount = formatLimitsDollars(change.Before.Get("threshold_amount").Int())
		}
		add("Monthly USD", amount)
		if change.Kind != "limit_set" {
			var recipients []string
			for _, value := range change.Before.Get("notification_channel.recipients").Array() {
				recipients = append(recipients, value.String())
			}
			add("Email recipients (comma separated)", strings.Join(recipients, ", "))
			add("Subject prefix", change.Before.Get("notification_channel.subject_prefix").String())
		}
	case "rate_update":
		for _, field := range limitsRateInputs {
			value := change.Before.Get(field.key)
			text := ""
			if value.Type == gjson.Number {
				text = value.Raw
			}
			add(field.label+" (blank: unchanged)", text)
		}
	}
	for i, input := range d.inputs {
		if input.Value() != d.initial[i] {
			return d, errors.New("This record contains text that the form cannot preserve. Inspect the original API record.")
		}
	}
	return d, nil
}

func (d limitsDraft) validate() (limitsChange, error) {
	c := d.change
	switch c.Kind {
	case "limit_set", "alert_create", "alert_update":
		minimum := int64(0)
		if c.Kind == "limit_set" {
			minimum = 1
		}
		amount, err := parseLimitsDollars(d.inputs[0].Value(), minimum)
		if err != nil {
			return c, err
		}
		c.Amount = amount
		if c.Kind != "limit_set" {
			c.Recipients, err = parseLimitsRecipients(d.inputs[1].Value())
			if err != nil {
				return c, err
			}
			value := d.inputs[2].Value()
			switch {
			case d.prefixMode == 2:
				c.SubjectPrefixSet, c.SubjectPrefix = true, nil
			case d.prefixMode == 1 || value != d.initial[2]:
				c.SubjectPrefixSet, c.SubjectPrefix = true, &value
			default:
				prefix := c.Before.Get("notification_channel.subject_prefix")
				c.SubjectPrefixSet = prefix.Exists()
				if prefix.Type == gjson.String {
					s := prefix.String()
					c.SubjectPrefix = &s
				}
			}
		}
	case "rate_update":
		c.RateValues = map[string]int64{}
		for i, field := range limitsRateInputs {
			value := d.inputs[i].Value()
			if value == "" || value == d.initial[i] {
				continue
			}
			digits := strings.TrimPrefix(value, "-")
			if digits == "" {
				return c, fmt.Errorf("%s needs an integer.", field.label)
			}
			for _, ch := range digits {
				if ch < '0' || ch > '9' {
					return c, fmt.Errorf("%s needs an integer.", field.label)
				}
			}
			number, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return c, fmt.Errorf("%s exceeds the supported integer range.", field.label)
			}
			c.RateValues[field.key] = number
		}
		if len(c.RateValues) == 0 {
			return c, errors.New("Change at least one rate value.")
		}
	}
	return c, nil
}

func limitsChangeTitle(kind string) string {
	switch kind {
	case "limit_set":
		return "Set hard spend limit"
	case "limit_delete":
		return "Remove hard spend limit"
	case "alert_create":
		return "Create spend alert"
	case "alert_update":
		return "Edit spend alert"
	case "alert_delete":
		return "Delete spend alert"
	case "rate_update":
		return "Update project rate limits"
	}
	return "Review change"
}

func limitsChangeBody(c limitsChange) map[string]any {
	body := map[string]any{}
	if c.Kind == "limit_set" || c.Kind == "alert_create" || c.Kind == "alert_update" {
		body["currency"], body["interval"], body["threshold_amount"] = "USD", "month", c.Amount
	}
	if c.Kind == "alert_create" || c.Kind == "alert_update" {
		channel := map[string]any{"type": "email", "recipients": c.Recipients}
		if c.SubjectPrefixSet {
			channel["subject_prefix"] = c.SubjectPrefix
		}
		body["notification_channel"] = channel
	}
	for name, value := range c.RateValues {
		body[name] = value
	}
	return body
}

func limitsReview(c limitsChange, projectID string, requestArgs []string, omit bool, shell string, executable ...string) string {
	scope := "Organization"
	if projectID != "" {
		scope = "Project " + projectID
	}
	before := "Current limit not confirmed"
	if c.Before.Exists() {
		before = c.Before.Raw
	}
	if c.Kind == "alert_create" {
		before = "New alert"
	}
	after, _ := json.MarshalIndent(limitsChangeBody(c), "", "  ")
	result := scope + "\n" + limitsChangeTitle(c.Kind) + "\n"
	if c.ID != "" {
		result += "ID: " + c.ID + "\n"
	}
	if c.Kind == "limit_set" || c.Kind == "limit_delete" || strings.HasPrefix(c.Kind, "alert_") {
		label, previous := "Hard spend limit", "Not confirmed"
		if strings.HasPrefix(c.Kind, "alert_") {
			label = "Alert threshold"
		}
		if c.Before.Exists() {
			previous = limitsThresholdLabel(c.Before)
		} else if c.Kind == "alert_create" {
			previous = "New alert"
		}
		next := "USD " + formatLimitsDollars(c.Amount) + " / month"
		if strings.HasSuffix(c.Kind, "_delete") {
			next = "Remove this resource"
		}
		result += "\n" + label + "\nBefore: " + previous + "\nAfter: " + next + "\n"
	}
	result += "\nBefore\n" + before + "\n\nAfter\n"
	if strings.HasSuffix(c.Kind, "_delete") {
		result += "Remove this resource.\n"
	} else {
		result += string(after) + "\n"
	}
	switch c.Kind {
	case "limit_set":
		result += "\nHard limit: USD " + formatLimitsDollars(c.Amount) + " / month. Enforcement can be delayed.\n"
		if c.BeforeMissing {
			result += "The current limit could not be confirmed. This action creates or replaces the hard limit.\n"
		}
	case "limit_delete":
		result += "\nThis removes the hard limit. The public API does not guarantee soft-budget behavior.\nInspect soft budgets: https://platform.openai.com/settings/organization/limits\n"
	case "alert_create", "alert_update":
		result += "\nAlerts notify recipients. Alerts do not enforce a spending cap.\n"
	case "rate_update":
		result += "\nOnly changed rate fields are sent. Zero has no assumed unlimited meaning.\n"
	}
	result += "\nThe CLI checks the current record before saving. This check is not atomic.\n"
	result += "The workspace sends this change once, without automatic retries.\n"
	result = readable.Text(result)
	command, reason := limitsCommand(c, projectID, requestArgs, omit, shell, executable...)
	if command == "" {
		return result + "\n" + reason + "\n"
	}
	return result + "\nCommand preview (inspection only)\n" + command + "\n\nCtrl+P prints the complete command and exits.\nThis command skips the workspace preflight and can retry automatically.\nRecheck current settings before running it.\n"
}

// Keep executable bytes separate from prose and viewport wrapping.
func limitsCommand(c limitsChange, projectID string, requestArgs []string, omit bool, shell string, executable ...string) (string, string) {
	if omit {
		return "", "Command preview omitted: request context cannot be reproduced safely."
	}
	args := append([]string{}, requestArgs...)
	args = append(args, "admin", "organization")
	if projectID != "" {
		args = append(args, "projects")
	}
	switch c.Kind {
	case "limit_set":
		args = append(args, "spend-limit", "update")
	case "limit_delete":
		args = append(args, "spend-limit", "delete")
	case "alert_create":
		args = append(args, "spend-alerts", "create")
	case "alert_update":
		args = append(args, "spend-alerts", "update")
	case "alert_delete":
		args = append(args, "spend-alerts", "delete")
	case "rate_update":
		args = append(args, "rate-limits", "update-rate-limit")
	}
	if projectID != "" {
		args = append(args, "--project-id", projectID)
	}
	if c.ID != "" {
		flag := "--alert-id"
		if c.Kind == "rate_update" {
			flag = "--rate-limit-id"
		}
		args = append(args, flag, c.ID)
	}
	if c.Kind == "limit_set" || c.Kind == "alert_create" || c.Kind == "alert_update" {
		args = append(args, "--currency", "USD", "--interval", "month", "--threshold-amount", strconv.FormatInt(c.Amount, 10))
	}
	if c.Kind == "alert_create" || c.Kind == "alert_update" {
		channelValues := limitsChangeBody(c)["notification_channel"].(map[string]any)
		if c.SubjectPrefixSet && c.SubjectPrefix != nil {
			prefix := *c.SubjectPrefix
			if strings.HasPrefix(prefix, "\\@") {
				return "", "Command preview omitted: the request parser cannot preserve this literal prefix."
			}
			if strings.HasPrefix(prefix, "@") {
				channelValues["subject_prefix"] = "\\" + prefix
			}
		}
		channel, _ := json.Marshal(channelValues)
		args = append(args, "--notification-channel", string(channel))
	}
	for _, field := range limitsRateInputs {
		if value, ok := c.RateValues[field.key]; ok {
			args = append(args, "--"+strings.ReplaceAll(field.key, "_", "-"), strconv.FormatInt(value, 10))
		}
	}
	command := terminal.FormatCommand(args, shell)
	if command == "" {
		return "", "Command printing needs Bash, zsh, fish or PowerShell 7."
	}
	if len(executable) > 0 && executable[0] != "" {
		command = terminal.ShellQuoter(shell)(executable[0]) + strings.TrimPrefix(command, "openai")
		if shell == "pwsh" {
			command = "& " + command
		}
	}
	return command, ""
}

// Use the generated operation identity so successful receipts retain projections.
func limitsChangeOperation(kind string, project bool) string {
	resource := "admin.organization."
	if project {
		resource += "projects."
	}
	method := ""
	switch kind {
	case "limit_set":
		resource, method = resource+"spend_limit", "update"
	case "limit_delete":
		resource, method = resource+"spend_limit", "delete"
	case "alert_create":
		resource, method = resource+"spend_alerts", "create"
	case "alert_update":
		resource, method = resource+"spend_alerts", "update"
	case "alert_delete":
		resource, method = resource+"spend_alerts", "delete"
	case "rate_update":
		resource, method = resource+"rate_limits", "update_rate_limit"
	default:
		return ""
	}
	return "(resource) " + resource + " > (method) " + method
}
