package transformers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// Spend thresholds use cents. Costs API amounts have a separate contract.
func selectSpendThresholdTransformer(route Route) Transformer {
	var object string
	alert := false
	switch route {
	case Route{"(resource) admin.organization.spend_limit > (method) retrieve", OutputResponse},
		Route{"(resource) admin.organization.spend_limit > (method) update", OutputResponse}:
		object = "organization.spend_limit"
	case Route{"(resource) admin.organization.projects.spend_limit > (method) retrieve", OutputResponse},
		Route{"(resource) admin.organization.projects.spend_limit > (method) update", OutputResponse}:
		object = "project.spend_limit"
	case Route{"(resource) admin.organization.spend_alerts > (method) create", OutputResponse},
		Route{"(resource) admin.organization.spend_alerts > (method) retrieve", OutputResponse},
		Route{"(resource) admin.organization.spend_alerts > (method) update", OutputResponse},
		Route{"(resource) admin.organization.spend_alerts > (method) list", OutputPageItem}:
		object, alert = "organization.spend_alert", true
	case Route{"(resource) admin.organization.projects.spend_alerts > (method) create", OutputResponse},
		Route{"(resource) admin.organization.projects.spend_alerts > (method) retrieve", OutputResponse},
		Route{"(resource) admin.organization.projects.spend_alerts > (method) update", OutputResponse},
		Route{"(resource) admin.organization.projects.spend_alerts > (method) list", OutputPageItem}:
		object, alert = "project.spend_alert", true
	default:
		return nil
	}
	return func(ctx context.Context, value gjson.Result) (gjson.Result, error) {
		return projectSpendThreshold(ctx, value, object, alert)
	}
}

func projectSpendThreshold(ctx context.Context, value gjson.Result, object string, alert bool) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !value.IsObject() || value.Get("object").String() != object {
		return value, nil
	}
	amount := value.Get("threshold_amount")
	summary := ""
	var summaryJSON []byte
	// Do not overwrite an unfamiliar response field with a derived field.
	if amount.Type == gjson.Number && !value.Get("spend_threshold").Exists() {
		var err error
		summary, err = spendThresholdText(ctx, amount.Raw, value.Get("currency"), value.Get("interval"))
		if err != nil {
			return gjson.Result{}, err
		}
		summaryJSON, err = json.Marshal(summary)
		if err != nil {
			return gjson.Result{}, err
		}
	}
	var out strings.Builder
	out.WriteByte('{')
	first := true
	write := func(key, raw string) {
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.WriteString(key)
		out.WriteByte(':')
		out.WriteString(raw)
	}
	value.ForEach(func(key, field gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		switch {
		case key.Str == "threshold_amount" && summary != "":
			write(`"spend_threshold"`, string(summaryJSON))
		case key.Str == "enforcement" && !alert && field.Type == gjson.Null:
			write(key.Raw, `"not reported in this response"`)
		default:
			// Preserve unknown fields and number spellings without decoding floats.
			write(key.Raw, field.Raw)
		}
		return true
	})
	if alert {
		if !value.Get("alert_behavior").Exists() {
			write(`"alert_behavior"`, `"Alerts notify; they are not spending caps."`)
		}
	} else if !value.Get("enforcement").Exists() {
		write(`"enforcement"`, `"not reported in this response"`)
	}
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	out.WriteByte('}')
	return gjson.Parse(out.String()), nil
}

func spendThresholdText(ctx context.Context, amount string, currency, interval gjson.Result) (string, error) {
	text := amount + " cents"
	if currency.Type == gjson.String && currency.Str == "USD" {
		decimal, err := spendCentsDecimal(ctx, amount)
		if err != nil {
			return "", err
		}
		if decimal != "" {
			text = "USD " + decimal
		} else {
			text += " (currency: USD)"
		}
	} else {
		text += " (currency: " + spendReportedUnit(currency) + ")"
	}
	if interval.Type == gjson.String && interval.Str == "month" {
		text += " per month"
	} else {
		text += " (interval: " + spendReportedUnit(interval) + ")"
	}
	return text, ctx.Err()
}

func spendReportedUnit(value gjson.Result) string {
	if value.Type == gjson.Null || value.Type == gjson.String && value.Str == "" {
		return "not reported"
	}
	if value.Type == gjson.String {
		return value.Str
	}
	return "see response field"
}

// Insert a decimal point in integer cents without float conversion or bounds.
// Unexpected fractions and exponents retain their original spelling in cents.
func spendCentsDecimal(ctx context.Context, amount string) (string, error) {
	sign := ""
	if strings.HasPrefix(amount, "-") {
		sign, amount = "-", amount[1:]
	}
	if amount == "" {
		return "", nil
	}
	for i := 0; i < len(amount); i++ {
		if i%32768 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if amount[i] < '0' || amount[i] > '9' {
			return "", nil
		}
	}
	switch len(amount) {
	case 1:
		return sign + "0.0" + amount, nil
	case 2:
		return sign + "0." + amount, nil
	default:
		return sign + amount[:len(amount)-2] + "." + amount[len(amount)-2:], nil
	}
}
