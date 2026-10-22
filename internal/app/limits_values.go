package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/mail"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// parseLimitsDollars converts explicit decimal USD input to integer cents.
func parseLimitsDollars(value string, minimum int64) (int64, error) {
	invalid := fmt.Errorf("Enter a USD amount using digits and at most two decimal places.")
	if value == "" {
		return 0, invalid
	}
	dollars, cents, decimal := strings.Cut(value, ".")
	if dollars == "" || decimal && (cents == "" || len(cents) > 2) {
		return 0, invalid
	}
	for _, part := range []string{dollars, cents} {
		for i := range len(part) {
			if part[i] < '0' || part[i] > '9' {
				return 0, invalid
			}
		}
	}
	whole, err := strconv.ParseUint(dollars, 10, 64)
	if err != nil || whole > uint64(math.MaxInt64)/100 {
		return 0, fmt.Errorf("The USD amount is too large.")
	}
	var fraction uint64
	if cents != "" {
		fraction = uint64(cents[0]-'0') * 10
		if len(cents) == 2 {
			fraction += uint64(cents[1] - '0')
		}
	}
	if whole*100 > uint64(math.MaxInt64)-fraction {
		return 0, fmt.Errorf("The USD amount is too large.")
	}
	result := int64(whole*100 + fraction)
	if result < minimum {
		return 0, fmt.Errorf("The USD amount must be at least %s.", formatLimitsDollars(minimum))
	}
	return result, nil
}

func formatLimitsDollars(cents int64) string {
	digits := strconv.FormatInt(cents, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	for len(digits) < 3 {
		digits = "0" + digits
	}
	return sign + digits[:len(digits)-2] + "." + digits[len(digits)-2:]
}

func parseLimitsRecipients(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	recipients := make([]string, 0, len(parts))
	for _, part := range parts {
		address := strings.TrimSpace(part)
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Name != "" || parsed.Address != address || strings.ContainsAny(address, "\r\n") {
			return nil, fmt.Errorf("Enter email addresses separated by commas, without display names.")
		}
		recipients = append(recipients, address)
	}
	return recipients, nil
}

func limitsUniqueObject(value gjson.Result) error {
	return limitsUniqueObjectContext(context.Background(), value)
}

func limitsUniqueObjectContext(ctx context.Context, value gjson.Result) error {
	seen := map[string]bool{}
	var err error
	value.ForEach(func(key, _ gjson.Result) bool {
		if err = ctx.Err(); err != nil {
			return false
		}
		if seen[key.Str] {
			err = errors.New("The response has duplicate fields. Use the original API command to inspect it.")
			return false
		}
		seen[key.Str] = true
		return true
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func limitsUniqueJSON(value gjson.Result) error {
	return limitsUniqueJSONContext(context.Background(), value)
}

func limitsUniqueJSONContext(ctx context.Context, value gjson.Result) error {
	pending := []gjson.Result{value}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if item.IsObject() {
			if err := limitsUniqueObjectContext(ctx, item); err != nil {
				return err
			}
		}
		if item.IsObject() || item.IsArray() {
			item.ForEach(func(_, field gjson.Result) bool {
				if ctx.Err() != nil {
					return false
				}
				if field.IsObject() || field.IsArray() {
					pending = append(pending, field)
				}
				return true
			})
		}
	}
	return ctx.Err()
}

func limitsKnownFields(value gjson.Result, fields ...string) bool {
	known := make(map[string]bool, len(fields))
	for _, field := range fields {
		known[field] = true
	}
	valid := true
	value.ForEach(func(key, _ gjson.Result) bool {
		valid = known[key.Str]
		return valid
	})
	return valid
}

func limitsInteger(value gjson.Result) (int64, error) {
	if value.Type != gjson.Number {
		return 0, errors.New("The returned amount or rate is not an integer.")
	}
	integer, err := strconv.ParseInt(value.Raw, 10, 64)
	if err != nil {
		return 0, errors.New("The returned amount or rate cannot be represented by this form.")
	}
	return integer, nil
}

func limitsEditableBaseline(value gjson.Result, kind string) error {
	return limitsEditableBaselineContext(context.Background(), value, kind)
}

func limitsEditableBaselineContext(ctx context.Context, value gjson.Result, kind string) error {
	if !value.IsObject() || !gjson.Valid(value.Raw) {
		return errors.New("The returned configuration cannot be edited by this form.")
	}
	if err := limitsUniqueJSONContext(ctx, value); err != nil {
		return err
	}
	object := value.Get("object")
	if object.Type != gjson.String {
		return errors.New("The returned configuration has no recognized object type.")
	}
	switch kind {
	case "limit_set", "limit_delete":
		if object.Str != "organization.spend_limit" && object.Str != "project.spend_limit" {
			return errors.New("The returned object is not a spend limit.")
		}
		if kind == "limit_delete" {
			return nil
		}
		if !limitsKnownFields(value, "object", "threshold_amount", "currency", "interval", "enforcement") {
			return errors.New("This spend limit contains unfamiliar configuration. Use the original API command to edit it.")
		}
		enforcement := value.Get("enforcement")
		if enforcement.Exists() && enforcement.Type != gjson.Null {
			status := enforcement.Get("status")
			if !enforcement.IsObject() || !limitsKnownFields(enforcement, "status") || status.Type != gjson.String ||
				status.Str != "inactive" && status.Str != "enforcing" {
				return errors.New("This spend limit has unfamiliar enforcement configuration. Use the original API command to edit it.")
			}
		}
	case "alert_update", "alert_delete":
		if object.Str != "organization.spend_alert" && object.Str != "project.spend_alert" {
			return errors.New("The returned object is not a spend alert.")
		}
		if value.Get("id").Type != gjson.String || value.Get("id").Str == "" {
			return errors.New("The returned spend alert has no identifier.")
		}
		if kind == "alert_delete" {
			return nil
		}
		if !limitsKnownFields(value, "id", "object", "threshold_amount", "currency", "interval", "notification_channel") {
			return errors.New("This alert contains unfamiliar configuration. Use the original API command to edit it.")
		}
		channel := value.Get("notification_channel")
		if !channel.IsObject() || !limitsKnownFields(channel, "type", "recipients", "subject_prefix") || channel.Get("type").Str != "email" {
			return errors.New("This alert has unfamiliar notification settings. Use the original API command to edit it.")
		}
		recipients := channel.Get("recipients")
		if !recipients.IsArray() || len(recipients.Array()) == 0 {
			return errors.New("This alert has no editable email recipients.")
		}
		for _, address := range recipients.Array() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if address.Type != gjson.String {
				return errors.New("This alert has an unrecognized email recipient.")
			}
			parsed, err := parseLimitsRecipients(address.Str)
			if err != nil || len(parsed) != 1 || parsed[0] != address.Str {
				return errors.New("This alert has an unrecognized email recipient.")
			}
		}
		prefix := channel.Get("subject_prefix")
		if prefix.Exists() && prefix.Type != gjson.Null && prefix.Type != gjson.String {
			return errors.New("This alert has an unrecognized email subject prefix.")
		}
	case "rate_update":
		if object.Str != "project.rate_limit" || value.Get("id").Type != gjson.String || value.Get("id").Str == "" ||
			value.Get("model").Type != gjson.String || value.Get("model").Str == "" {
			return errors.New("The returned rate limit has no recognized identity.")
		}
		if limitsRateReadOnly(value) {
			return errors.New("This model's rate limit is read-only. Inspect the original API record for its current values.")
		}
		available := false
		for _, field := range limitsRateFields {
			item := value.Get(field)
			if item.Exists() && item.Type != gjson.Null {
				if _, err := limitsInteger(item); err != nil {
					return err
				}
				available = true
			}
		}
		if !available {
			return errors.New("This record has no editable numeric rate fields. Inspect the original API record.")
		}
		return nil
	default:
		return errors.New("Unknown limits edit operation.")
	}
	if value.Get("currency").Str != "USD" || value.Get("interval").Str != "month" {
		return errors.New("This form can edit only returned monthly USD configurations.")
	}
	_, err := limitsInteger(value.Get("threshold_amount"))
	return err
}

var limitsRateFields = []string{
	"max_requests_per_1_minute", "max_tokens_per_1_minute", "max_requests_per_1_day",
	"max_images_per_1_minute", "max_audio_megabytes_per_1_minute", "batch_1_day_max_input_tokens",
	"max_tokens_per_1_day",
}

func limitsRateReadOnly(value gjson.Result) bool {
	model := value.Get("model").Str
	return strings.HasPrefix(model, "ft:") || strings.HasSuffix(model, "-shared")
}

func limitsValidateChange(change limitsChange) error {
	return limitsValidateChangeContext(context.Background(), change)
}

func limitsValidateChangeContext(ctx context.Context, change limitsChange) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if change.BeforeMissing && change.Before.Exists() {
		return errors.New("The displayed configuration has an inconsistent presence state. Refresh before editing.")
	}
	switch change.Kind {
	case "limit_set":
		if change.Amount < 1 {
			return errors.New("A hard spend limit must be at least USD 0.01.")
		}
	case "alert_create", "alert_update":
		if change.Amount < 0 {
			return errors.New("An alert threshold cannot be negative.")
		}
		if len(change.Recipients) == 0 {
			return errors.New("An alert requires at least one email recipient.")
		}
		for _, address := range change.Recipients {
			if err := ctx.Err(); err != nil {
				return err
			}
			parsed, err := parseLimitsRecipients(address)
			if err != nil || len(parsed) != 1 || parsed[0] != address {
				return errors.New("Enter valid email recipients without display names.")
			}
		}
	case "rate_update":
		if len(change.RateValues) == 0 {
			return errors.New("Set at least one rate-limit value.")
		}
		for key, value := range change.RateValues {
			known := false
			for _, field := range limitsRateFields {
				known = known || key == field
			}
			if !known {
				return errors.New("This form does not support the requested rate-limit field.")
			}
			if value < 0 {
				return errors.New("Rate values cannot be negative. Enter zero or a positive integer.")
			}
			if change.Before.Get(key).Type != gjson.Number {
				return errors.New("This rate field was not returned for this model. Inspect the original API record before editing.")
			}
		}
	case "limit_delete", "alert_delete":
	default:
		return errors.New("Unknown limits operation.")
	}
	if change.Kind == "alert_create" || change.Kind == "limit_set" && change.BeforeMissing {
		return nil
	}
	if err := limitsEditableBaselineContext(ctx, change.Before, change.Kind); err != nil {
		return err
	}
	if change.Kind == "alert_update" || change.Kind == "alert_delete" || change.Kind == "rate_update" {
		if change.ID == "" || change.Before.Get("id").Str != change.ID {
			return errors.New("The selected identifier does not match the displayed configuration.")
		}
	}
	return nil
}

func limitsCanonicalJSON(value gjson.Result) (string, error) {
	return limitsCanonicalJSONContext(context.Background(), value)
}

func limitsCanonicalJSONContext(ctx context.Context, value gjson.Result) (string, error) {
	if err := limitsUniqueJSONContext(ctx, value); err != nil {
		return "", err
	}
	decoder := json.NewDecoder(strings.NewReader(value.Raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", errors.New("The configuration cannot be compared safely.")
	}
	encoded, err := json.Marshal(decoded)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return string(encoded), err
}

// limitsCostTotal summarizes raw USD amounts, never integer threshold cents.
// Unsupported records remain in the snapshot even when a total cannot be calculated.
func limitsCostTotal(ctx context.Context, buckets []gjson.Result, start, end int64, projectID string) (string, error) {
	total := new(big.Rat)
	precision := 2
	var ranges [][2]int64
	invalid := &limitsCostSummaryError{Reason: "invalid_bucket"}
	for _, bucket := range buckets {
		if err := limitsUniqueJSONContext(ctx, bucket); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", &limitsCostSummaryError{Reason: "ambiguous_fields"}
		}
		if !bucket.IsObject() || bucket.Get("object").Str != "bucket" || !bucket.Get("results").IsArray() {
			return "", invalid
		}
		from, fromErr := limitsInteger(bucket.Get("start_time"))
		to, toErr := limitsInteger(bucket.Get("end_time"))
		if fromErr != nil || toErr != nil || from < start || to > end || from >= to || to-from > 86400 {
			return "", invalid
		}
		for _, previous := range ranges {
			if from < previous[1] && previous[0] < to {
				return "", &limitsCostSummaryError{Reason: "overlapping_buckets"}
			}
		}
		ranges = append(ranges, [2]int64{from, to})
		var resultErr error
		bucket.Get("results").ForEach(func(_, result gjson.Result) bool {
			if resultErr = ctx.Err(); resultErr != nil {
				return false
			}
			if !result.IsObject() || result.Get("object").Str != "organization.costs.result" {
				resultErr = &limitsCostSummaryError{Reason: "unexpected_result"}
				return false
			}
			if result.Get("amount.currency").Type != gjson.String || !strings.EqualFold(result.Get("amount.currency").Str, "USD") {
				resultErr = &limitsCostSummaryError{Reason: "unsupported_currency"}
				return false
			}
			project := result.Get("project_id")
			if projectID != "" && project.Exists() && project.Type != gjson.Null && (project.Type != gjson.String || project.Str != projectID) {
				resultErr = &limitsCostSummaryError{Reason: "unexpected_project"}
				return false
			}
			amount, scale, err := limitsCostDecimal(result.Get("amount.value"))
			if err != nil {
				resultErr = err
				return false
			}
			total.Add(total, amount)
			precision = max(precision, scale)
			return true
		})
		if resultErr != nil {
			return "", resultErr
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return total.FloatString(precision), nil
}

func limitsCostDecimal(value gjson.Result) (*big.Rat, int, error) {
	invalid := &limitsCostSummaryError{Reason: "invalid_amount"}
	if value.Type != gjson.Number || !gjson.Valid(value.Raw) {
		return nil, 0, invalid
	}
	coefficient := value.Raw
	exponent := int64(0)
	if at := strings.IndexAny(coefficient, "eE"); at >= 0 {
		var err error
		exponent, err = strconv.ParseInt(coefficient[at+1:], 10, 64)
		if err != nil {
			return nil, 0, &limitsCostSummaryError{Reason: "unsupported_precision"}
		}
		coefficient = coefficient[:at]
	}
	// Bound only derived display arithmetic. Raw API records are never truncated or rejected.
	const summaryDigits = 10000
	if len(coefficient) > summaryDigits || exponent > summaryDigits || exponent < -summaryDigits {
		return nil, 0, &limitsCostSummaryError{Reason: "unsupported_precision"}
	}
	scale := int64(0)
	if at := strings.IndexByte(coefficient, '.'); at >= 0 {
		scale = int64(len(coefficient) - at - 1)
	}
	scale = max(0, scale-exponent)
	if scale > summaryDigits || int64(len(coefficient))+max(0, exponent) > summaryDigits {
		return nil, 0, &limitsCostSummaryError{Reason: "unsupported_precision"}
	}
	amount, ok := new(big.Rat).SetString(value.Raw)
	if !ok {
		return nil, 0, invalid
	}
	return amount, int(scale), nil
}
