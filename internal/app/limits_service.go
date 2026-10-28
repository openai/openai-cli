package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openai/openai-cli/internal/apiquery"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type limitsBackend interface {
	Load(context.Context) (limitsSnapshot, error)
	Mutate(context.Context, limitsChange) (gjson.Result, error)
}

type limitsSourceFailure struct {
	Resource string
	Err      error
}

type limitsCostSummaryError struct{ Reason string }

func (*limitsCostSummaryError) Error() string {
	return "Costs summary unavailable. Inspect costs.buckets or use the usage costs command."
}

type limitsMutationFailure struct {
	Err       error
	Attempted bool
}

func (failure *limitsMutationFailure) Error() string { return failure.Err.Error() }
func (failure *limitsMutationFailure) Unwrap() error { return failure.Err }

type limitsSnapshot struct {
	ProjectID        string
	Project          gjson.Result
	Limit            gjson.Result
	LimitAbsent      bool
	LimitUnconfirmed bool
	Alerts           []gjson.Result
	Rates            []gjson.Result
	AlertsComplete   bool
	RatesComplete    bool
	Costs            []gjson.Result
	CostsComplete    bool
	CostsTotal       string
	CostsStart       int64
	CostsEnd         int64
	Errors           []limitsSourceFailure
}

type limitsChange struct {
	Kind             string
	ID               string
	Before           gjson.Result
	BeforeMissing    bool
	Amount           int64
	Recipients       []string
	SubjectPrefix    *string
	SubjectPrefixSet bool
	RateValues       map[string]int64
}

type limitsAPIBackend struct {
	client     openai.Client
	projectID  string
	maxItems   int64
	mutationMu sync.Mutex
	now        func() time.Time
}

func newLimitsBackend(cmd *cli.Command, project bool) (limitsBackend, error) {
	maximum := cmd.Int64("max-items")
	if maximum < -1 {
		return nil, &localcommands.LocalError{Message: "--max-items must be -1, zero, or a positive integer."}
	}
	projectID := ""
	if project {
		projectID = cmd.String("project-id")
		if projectID == "" {
			return nil, &localcommands.LocalError{Message: "Set --project-id to inspect project limits."}
		}
	}
	options, err := FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
	if err != nil {
		return nil, err
	}
	options = append(GetDefaultRequestOptions(cmd), options...)
	return &limitsAPIBackend{client: openai.NewClient(options...), projectID: projectID, maxItems: maximum}, nil
}

func (snapshot limitsSnapshot) JSON() (gjson.Result, error) {
	raw := func(value gjson.Result) json.RawMessage {
		if !value.Exists() {
			return json.RawMessage("null")
		}
		return json.RawMessage(value.Raw)
	}
	array := func(items []gjson.Result) []json.RawMessage {
		result := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			result = append(result, raw(item))
		}
		return result
	}
	sourceErrors := make([]map[string]any, 0, len(snapshot.Errors))
	for _, failure := range snapshot.Errors {
		source := map[string]any{"resource": failure.Resource, "status": "request_failed"}
		var apiErr *openai.Error
		var summaryErr *limitsCostSummaryError
		if errors.Is(failure.Err, context.Canceled) || errors.Is(failure.Err, context.DeadlineExceeded) {
			source["status"] = "canceled"
		} else if errors.As(failure.Err, &apiErr) {
			source["status"] = "api_error"
			if apiErr.StatusCode > 0 {
				source["status_code"] = apiErr.StatusCode
			}
		} else if errors.As(failure.Err, &summaryErr) {
			source["status"] = "summary_unavailable"
		}
		if errors.As(failure.Err, &summaryErr) {
			source["summary_reason"] = summaryErr.Reason
		}
		sourceErrors = append(sourceErrors, source)
	}
	var configured any
	limitFailed := false
	for _, failure := range snapshot.Errors {
		limitFailed = limitFailed || failure.Resource == "spend_limit" || failure.Resource == "project"
	}
	if !limitFailed && snapshot.LimitAbsent {
		configured = false
	} else if !limitFailed && snapshot.Limit.Exists() {
		configured = true
	}
	view := map[string]any{
		"scope": "organization", "spend_limit": raw(snapshot.Limit), "spend_limit_configured": configured,
		"spend_alerts": array(snapshot.Alerts), "spend_alerts_complete": snapshot.AlertsComplete,
		"source_errors": sourceErrors,
	}
	var costTotal any
	if snapshot.CostsTotal != "" {
		costTotal = snapshot.CostsTotal
	}
	view["costs"] = map[string]any{
		"start_time": snapshot.CostsStart, "end_time": snapshot.CostsEnd, "timezone": "UTC",
		"currency": "USD", "total": costTotal, "complete": snapshot.CostsComplete, "buckets": array(snapshot.Costs),
	}
	if snapshot.ProjectID != "" {
		view["scope"], view["project_id"], view["project"] = "project", snapshot.ProjectID, raw(snapshot.Project)
		view["project_rate_limits"], view["project_rate_limits_complete"] = array(snapshot.Rates), snapshot.RatesComplete
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(encoded), nil
}

func (backend *limitsAPIBackend) Load(ctx context.Context) (limitsSnapshot, error) {
	snapshot := limitsSnapshot{ProjectID: backend.projectID}
	var failures []error
	record := func(resource string, err error) {
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, limitsSourceFailure{Resource: resource, Err: err})
			failures = append(failures, fmt.Errorf("%s: %w", resource, err))
		}
	}
	projectVerified := backend.projectID == ""
	var err error
	if backend.projectID != "" {
		snapshot.Project, err = backend.get(ctx, "project", "")
		record("project", err)
		projectVerified = err == nil
	}
	if projectVerified {
		snapshot.Limit, err = backend.get(ctx, "spend_limit", "")
		if limitsHTTPStatus(err, http.StatusNotFound) {
			// HTTP 404 does not distinguish absent configuration from endpoint or access errors.
			snapshot.LimitUnconfirmed = true
		}
		record("spend_limit", err)
	}
	snapshot.Alerts, snapshot.AlertsComplete, err = backend.list(ctx, false)
	record("spend_alerts", err)
	if backend.projectID != "" {
		snapshot.Rates, snapshot.RatesComplete, err = backend.list(ctx, true)
		record("project_rate_limits", err)
	}
	now := time.Now().UTC()
	if backend.now != nil {
		now = backend.now().UTC()
	}
	snapshot.CostsStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	snapshot.CostsEnd = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Unix()
	snapshot.Costs, snapshot.CostsComplete, err = backend.costs(ctx, snapshot.CostsStart, snapshot.CostsEnd)
	if len(snapshot.Costs) != 0 || snapshot.CostsComplete {
		var totalErr error
		snapshot.CostsTotal, totalErr = limitsCostTotal(ctx, snapshot.Costs, snapshot.CostsStart, snapshot.CostsEnd, backend.projectID)
		err = errors.Join(err, totalErr)
		if totalErr != nil {
			snapshot.CostsComplete = false
		}
	}
	record("costs", err)
	return snapshot, errors.Join(failures...)
}

// Costs returns raw daily buckets. The independent summary never changes those records.
func (backend *limitsAPIBackend) costs(ctx context.Context, start, end int64) ([]gjson.Result, bool, error) {
	items := []gjson.Result{}
	cursor := ""
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return items, false, err
		}
		pageSize := int64(100)
		if backend.maxItems >= 0 {
			pageSize = max(1, min(pageSize, backend.maxItems-int64(len(items))))
		}
		params := openai.AdminOrganizationUsageCostsParams{
			StartTime: start, EndTime: openai.Int(end), Limit: openai.Int(pageSize), BucketWidth: "1d",
		}
		if backend.projectID != "" {
			params.ProjectIDs = []string{backend.projectID}
		}
		if cursor != "" {
			params.Page = openai.String(cursor)
		}
		var body []byte
		_, err := backend.client.Admin.Organization.Usage.Costs(ctx, params, option.WithResponseBodyInto(&body))
		page, err := limitsResponse(body, err)
		if err != nil {
			return items, false, err
		}
		if err := limitsUniqueObjectContext(ctx, page); err != nil {
			return items, false, err
		}
		data, more := page.Get("data"), page.Get("has_more")
		if page.Get("object").Str != "page" || !data.IsArray() || more.Type != gjson.True && more.Type != gjson.False {
			return items, false, errors.New("The Costs response is missing its buckets or pagination state.")
		}
		truncated := false
		var readErr error
		count := 0
		data.ForEach(func(_, item gjson.Result) bool {
			count++
			if readErr = ctx.Err(); readErr != nil {
				return false
			}
			if backend.maxItems >= 0 && int64(len(items)) >= backend.maxItems {
				truncated = true
				return false
			}
			items = append(items, item)
			return true
		})
		if readErr != nil {
			return items, false, readErr
		}
		if truncated || backend.maxItems >= 0 && int64(len(items)) >= backend.maxItems && more.Bool() {
			return items, false, nil
		}
		if !more.Bool() {
			return items, true, nil
		}
		next := page.Get("next_page")
		if count == 0 || next.Type != gjson.String || next.Str == "" || seen[next.Str] || next.Str == cursor {
			return items, false, errors.New("The Costs pagination cursor is missing, repeated, or cannot advance.")
		}
		cursor, seen[next.Str] = next.Str, true
	}
}

func limitsHTTPStatus(err error, status int) bool {
	var apiErr *openai.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

func limitsResponse(raw []byte, err error) (gjson.Result, error) {
	if err != nil {
		return gjson.Result{}, err
	}
	if !gjson.ValidBytes(raw) {
		return gjson.Result{}, errors.New("The limits response contains invalid JSON.")
	}
	value := gjson.ParseBytes(raw)
	if !value.IsObject() {
		return gjson.Result{}, errors.New("The limits response must contain an object.")
	}
	return value, nil
}

func (backend *limitsAPIBackend) get(ctx context.Context, resource, id string) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	var body []byte
	options := []option.RequestOption{option.WithResponseBodyInto(&body)}
	var err error
	switch resource {
	case "project":
		_, err = backend.client.Admin.Organization.Projects.Get(ctx, backend.projectID, options...)
	case "spend_limit":
		if backend.projectID == "" {
			_, err = backend.client.Admin.Organization.SpendLimit.Get(ctx, options...)
		} else {
			_, err = backend.client.Admin.Organization.Projects.SpendLimit.Get(ctx, backend.projectID, options...)
		}
	case "spend_alert":
		if backend.projectID == "" {
			_, err = backend.client.Admin.Organization.SpendAlerts.Get(ctx, id, options...)
		} else {
			_, err = backend.client.Admin.Organization.Projects.SpendAlerts.Get(ctx, backend.projectID, id, options...)
		}
	default:
		return gjson.Result{}, errors.New("Unknown limits resource.")
	}
	value, err := limitsResponse(body, err)
	if err == nil {
		expectedObject := "organization." + resource
		if backend.projectID != "" && resource != "project" {
			expectedObject = "project." + resource
		}
		if value.Get("object").Str != expectedObject {
			err = errors.New("The limits response has an unexpected object type.")
		} else if resource == "project" {
			err = limitsUniqueObjectContext(ctx, value)
			if err == nil && value.Get("id").Str != backend.projectID {
				err = errors.New("The project response does not identify the requested project.")
			}
		} else if resource == "spend_alert" && value.Get("id").Str != id {
			err = errors.New("The alert response does not identify the requested alert.")
		}
	}
	return value, err
}

func (backend *limitsAPIBackend) list(ctx context.Context, rates bool) ([]gjson.Result, bool, error) {
	items := []gjson.Result{}
	expectedObject := "organization.spend_alert"
	if rates {
		expectedObject = "project.rate_limit"
	} else if backend.projectID != "" {
		expectedObject = "project.spend_alert"
	}
	after := ""
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return items, false, err
		}
		pageSize := int64(100)
		if backend.maxItems >= 0 {
			pageSize = max(1, min(pageSize, backend.maxItems-int64(len(items))))
		}
		var body []byte
		options := []option.RequestOption{option.WithResponseBodyInto(&body)}
		var err error
		if rates {
			params := openai.AdminOrganizationProjectRateLimitListRateLimitsParams{Limit: openai.Int(pageSize)}
			if after != "" {
				params.After = openai.String(after)
			}
			_, err = backend.client.Admin.Organization.Projects.RateLimits.ListRateLimits(ctx, backend.projectID, params, options...)
		} else if backend.projectID == "" {
			params := openai.AdminOrganizationSpendAlertListParams{Limit: openai.Int(pageSize)}
			if after != "" {
				params.After = openai.String(after)
			}
			_, err = backend.client.Admin.Organization.SpendAlerts.List(ctx, params, options...)
		} else {
			params := openai.AdminOrganizationProjectSpendAlertListParams{Limit: openai.Int(pageSize)}
			if after != "" {
				params.After = openai.String(after)
			}
			_, err = backend.client.Admin.Organization.Projects.SpendAlerts.List(ctx, backend.projectID, params, options...)
		}
		page, err := limitsResponse(body, err)
		if err != nil {
			return items, false, err
		}
		if err := limitsUniqueObjectContext(ctx, page); err != nil {
			return items, false, err
		}
		data, more := page.Get("data"), page.Get("has_more")
		if !data.IsArray() || more.Type != gjson.True && more.Type != gjson.False {
			return items, false, errors.New("The limits page is missing its records or pagination state.")
		}
		truncated := false
		var recordErr error
		data.ForEach(func(_, item gjson.Result) bool {
			if err := ctx.Err(); err != nil {
				recordErr = err
				return false
			}
			if backend.maxItems >= 0 && int64(len(items)) >= backend.maxItems {
				truncated = true
				return false
			}
			if !item.IsObject() {
				recordErr = errors.New("A limits page contains a non-object record.")
				return false
			}
			objects := 0
			item.ForEach(func(key, _ gjson.Result) bool {
				if recordErr = ctx.Err(); recordErr != nil {
					return false
				}
				if key.Str == "object" {
					objects++
				}
				return objects <= 1
			})
			if recordErr != nil {
				return false
			}
			object := item.Get("object")
			if objects != 1 || object.Type != gjson.String || object.Str != expectedObject {
				recordErr = errors.New("A limits page contains an unexpected resource or scope.")
				return false
			}
			items = append(items, item)
			return true
		})
		if recordErr != nil {
			return items, false, recordErr
		}
		if truncated || backend.maxItems >= 0 && int64(len(items)) >= backend.maxItems && more.Bool() {
			return items, false, nil
		}
		if !more.Bool() {
			return items, true, nil
		}
		cursor := page.Get("last_id")
		if cursor.Type != gjson.String || cursor.Str == "" || cursor.Str == after || seen[cursor.Str] {
			return items, false, errors.New("The limits pagination cursor is missing or repeated.")
		}
		if len(data.Array()) == 0 {
			return items, false, errors.New("The limits page cannot advance without records.")
		}
		after, seen[cursor.Str] = cursor.Str, true
	}
}

func (backend *limitsAPIBackend) Mutate(ctx context.Context, change limitsChange) (result gjson.Result, resultErr error) {
	attempted := false
	defer func() {
		if resultErr != nil {
			resultErr = &limitsMutationFailure{Err: resultErr, Attempted: attempted}
		}
	}()
	if !backend.mutationMu.TryLock() {
		return gjson.Result{}, errors.New("A limits change is already in progress.")
	}
	defer backend.mutationMu.Unlock()
	if err := backend.preflight(ctx, change); err != nil {
		return gjson.Result{}, err
	}
	var body []byte
	options := []option.RequestOption{option.WithMaxRetries(0), option.WithResponseBodyInto(&body)}
	var err error
	switch change.Kind {
	case "limit_set":
		attempted = true
		if backend.projectID == "" {
			_, err = backend.client.Admin.Organization.SpendLimit.Update(ctx, openai.AdminOrganizationSpendLimitUpdateParams{
				Currency: "USD", Interval: "month", ThresholdAmount: change.Amount,
			}, options...)
		} else {
			_, err = backend.client.Admin.Organization.Projects.SpendLimit.Update(ctx, backend.projectID, openai.AdminOrganizationProjectSpendLimitUpdateParams{
				Currency: "USD", Interval: "month", ThresholdAmount: change.Amount,
			}, options...)
		}
	case "limit_delete":
		attempted = true
		if backend.projectID == "" {
			_, err = backend.client.Admin.Organization.SpendLimit.Delete(ctx, options...)
		} else {
			_, err = backend.client.Admin.Organization.Projects.SpendLimit.Delete(ctx, backend.projectID, options...)
		}
	case "alert_create", "alert_update":
		payload, payloadErr := limitsAlertPayload(change)
		if payloadErr != nil {
			return gjson.Result{}, payloadErr
		}
		if backend.projectID == "" && change.Kind == "alert_create" {
			var params openai.AdminOrganizationSpendAlertNewParams
			if err = json.Unmarshal(payload, &params); err == nil {
				attempted = true
				_, err = backend.client.Admin.Organization.SpendAlerts.New(ctx, params, options...)
			}
		} else if backend.projectID == "" {
			var params openai.AdminOrganizationSpendAlertUpdateParams
			if err = json.Unmarshal(payload, &params); err == nil {
				attempted = true
				_, err = backend.client.Admin.Organization.SpendAlerts.Update(ctx, change.ID, params, options...)
			}
		} else if change.Kind == "alert_create" {
			var params openai.AdminOrganizationProjectSpendAlertNewParams
			if err = json.Unmarshal(payload, &params); err == nil {
				attempted = true
				_, err = backend.client.Admin.Organization.Projects.SpendAlerts.New(ctx, backend.projectID, params, options...)
			}
		} else {
			var params openai.AdminOrganizationProjectSpendAlertUpdateParams
			if err = json.Unmarshal(payload, &params); err == nil {
				attempted = true
				_, err = backend.client.Admin.Organization.Projects.SpendAlerts.Update(ctx, backend.projectID, change.ID, params, options...)
			}
		}
	case "alert_delete":
		attempted = true
		if backend.projectID == "" {
			_, err = backend.client.Admin.Organization.SpendAlerts.Delete(ctx, change.ID, options...)
		} else {
			_, err = backend.client.Admin.Organization.Projects.SpendAlerts.Delete(ctx, backend.projectID, change.ID, options...)
		}
	case "rate_update":
		params := openai.AdminOrganizationProjectRateLimitUpdateRateLimitParams{}
		for field, value := range change.RateValues {
			switch field {
			case "max_requests_per_1_minute":
				params.MaxRequestsPer1Minute = openai.Int(value)
			case "max_tokens_per_1_minute":
				params.MaxTokensPer1Minute = openai.Int(value)
			case "max_requests_per_1_day":
				params.MaxRequestsPer1Day = openai.Int(value)
			case "max_images_per_1_minute":
				params.MaxImagesPer1Minute = openai.Int(value)
			case "max_audio_megabytes_per_1_minute":
				params.MaxAudioMegabytesPer1Minute = openai.Int(value)
			case "batch_1_day_max_input_tokens":
				params.Batch1DayMaxInputTokens = openai.Int(value)
			case "max_tokens_per_1_day":
				options = append(options, option.WithJSONSet(field, value))
			}
		}
		attempted = true
		_, err = backend.client.Admin.Organization.Projects.RateLimits.UpdateRateLimit(ctx, backend.projectID, change.ID, params, options...)
	}
	value, responseErr := limitsResponse(body, err)
	if responseErr == nil {
		responseErr = limitsMutationConfirmedContext(ctx, value, change, backend.projectID != "")
	}
	return value, responseErr
}

func limitsMutationConfirmed(value gjson.Result, change limitsChange, project bool) error {
	return limitsMutationConfirmedContext(context.Background(), value, change, project)
}

func limitsMutationConfirmedContext(ctx context.Context, value gjson.Result, change limitsChange, project bool) error {
	scope := "organization"
	if project {
		scope = "project"
	}
	resource := "spend_limit"
	if change.Kind == "alert_create" || change.Kind == "alert_update" || change.Kind == "alert_delete" {
		resource = "spend_alert"
	}
	if change.Kind == "rate_update" {
		resource = "rate_limit"
	}
	deleting := change.Kind == "limit_delete" || change.Kind == "alert_delete"
	object := scope + "." + resource
	if deleting {
		object += ".deleted"
	}
	invalid := errors.New("The API returned an incomplete confirmation. Refresh and inspect the resource before another change.")
	if value.Get("object").Type != gjson.String || value.Get("object").Str != object {
		return invalid
	}
	if err := limitsUniqueJSONContext(ctx, value); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	if resource != "spend_limit" {
		id := value.Get("id")
		if id.Type != gjson.String || id.Str == "" || change.Kind != "alert_create" && id.Str != change.ID {
			return invalid
		}
	}
	if deleting {
		if value.Get("deleted").Type != gjson.True {
			return invalid
		}
		return ctx.Err()
	}
	if resource == "rate_limit" {
		if err := limitsEditableBaselineContext(ctx, value, "rate_update"); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return invalid
		}
	} else {
		if value.Get("threshold_amount").Type != gjson.Number || value.Get("currency").Type != gjson.String ||
			value.Get("currency").Str == "" || value.Get("interval").Type != gjson.String || value.Get("interval").Str == "" {
			return invalid
		}
		if resource == "spend_alert" {
			channel := value.Get("notification_channel")
			if !channel.IsObject() || channel.Get("type").Type != gjson.String || !channel.Get("recipients").IsArray() {
				return invalid
			}
		}
	}
	mismatch := errors.New("The API returned different values from the reviewed change. Inspect the returned record before another change.")
	if resource == "rate_limit" {
		if value.Get("model").Str != change.Before.Get("model").Str {
			return mismatch
		}
		for field, requested := range change.RateValues {
			if err := ctx.Err(); err != nil {
				return err
			}
			returned, err := limitsInteger(value.Get(field))
			if err != nil || returned != requested {
				return mismatch
			}
		}
	} else {
		returned, err := limitsInteger(value.Get("threshold_amount"))
		if err != nil || returned != change.Amount || value.Get("currency").Str != "USD" || value.Get("interval").Str != "month" {
			return mismatch
		}
		if resource == "spend_alert" {
			channel := value.Get("notification_channel")
			if channel.Get("type").Str != "email" {
				return mismatch
			}
			recipients := map[string]int{}
			for _, address := range change.Recipients {
				recipients[address]++
			}
			var recipientErr error
			channel.Get("recipients").ForEach(func(_, address gjson.Result) bool {
				if recipientErr = ctx.Err(); recipientErr != nil {
					return false
				}
				if address.Type != gjson.String || recipients[address.Str] == 0 {
					recipientErr = mismatch
					return false
				}
				recipients[address.Str]--
				return true
			})
			if recipientErr != nil {
				return recipientErr
			}
			for _, count := range recipients {
				if count != 0 {
					return mismatch
				}
			}
			if change.SubjectPrefixSet {
				prefix := channel.Get("subject_prefix")
				if !prefix.Exists() || change.SubjectPrefix == nil && prefix.Type != gjson.Null ||
					change.SubjectPrefix != nil && (prefix.Type != gjson.String || prefix.Str != *change.SubjectPrefix) {
					return mismatch
				}
			}
		}
	}
	return ctx.Err()
}

func (backend *limitsAPIBackend) preflight(ctx context.Context, change limitsChange) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := limitsValidateChangeContext(ctx, change); err != nil {
		return err
	}
	if backend.projectID != "" {
		if _, err := backend.get(ctx, "project", ""); err != nil {
			return err
		}
	}
	if change.Kind == "alert_create" {
		return ctx.Err()
	}
	var current gjson.Result
	var err error
	switch change.Kind {
	case "limit_set", "limit_delete":
		current, err = backend.get(ctx, "spend_limit", "")
		if change.Kind == "limit_set" && change.BeforeMissing && limitsHTTPStatus(err, http.StatusNotFound) {
			// BeforeMissing records explicit review of an unconfirmed baseline, not proven absence.
			return ctx.Err()
		}
	case "alert_update", "alert_delete":
		current, err = backend.get(ctx, "spend_alert", change.ID)
	case "rate_update":
		if backend.projectID == "" {
			return errors.New("Rate-limit changes require a project scope.")
		}
		var items []gjson.Result
		var complete bool
		items, complete, err = backend.list(ctx, true)
		if err == nil && !complete {
			return errors.New("Rate-limit editing requires a complete list. Reopen with a larger --max-items value or -1.")
		}
		for _, item := range items {
			if item.Get("id").Str == change.ID {
				if current.Exists() {
					return errors.New("The rate-limit ID appears more than once. Refresh before editing.")
				}
				current = item
			}
		}
	}
	if err != nil {
		return err
	}
	if change.BeforeMissing || !current.Exists() {
		return errors.New("The configuration changed. Refresh before applying this change.")
	}
	if err := limitsEditableBaselineContext(ctx, current, change.Kind); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := limitsCanonicalJSONContext(ctx, change.Before)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now, err := limitsCanonicalJSONContext(ctx, current)
	if err != nil {
		return err
	}
	if before != now {
		return errors.New("The configuration changed. Refresh before applying this change.")
	}
	return ctx.Err()
}

func limitsAlertPayload(change limitsChange) ([]byte, error) {
	channel := map[string]any{"type": "email", "recipients": change.Recipients}
	if change.SubjectPrefixSet {
		channel["subject_prefix"] = change.SubjectPrefix
	}
	return json.Marshal(map[string]any{
		"currency": "USD", "interval": "month", "threshold_amount": change.Amount, "notification_channel": channel,
	})
}
