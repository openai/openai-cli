package imagemodels

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type Status string

const (
	StatusVisible    Status = "visible"
	StatusNotVisible Status = "not_visible"
	StatusRetired    Status = "retired"
	StatusUnknown    Status = "unknown"
	StatusNotChecked Status = "not_checked"
)

// Failure contains only stable, safe categories, never request URLs, credentials,
// raw API messages, or account metadata.
type Failure string

const (
	FailureAuthentication  Failure = "authentication"
	FailureForbidden       Failure = "forbidden"
	FailureRateLimit       Failure = "rate_limit"
	FailureTimeout         Failure = "timeout"
	FailureServer          Failure = "server"
	FailureNetwork         Failure = "network"
	FailureCanceled        Failure = "canceled"
	FailureInvalidResponse Failure = "invalid_response"
	FailureRequest         Failure = "request"
)

// Result reports visibility of model metadata. A visible result does not prove
// generation permission, quota, or compatibility with every image option.
type Result struct {
	Entry
	Status       Status  `json:"status"`
	Failure      Failure `json:"failure,omitempty"`
	ShutdownDate string  `json:"shutdown_date,omitempty"`
}

// Discover makes at most three simultaneous per-model GET requests. Every
// request has a five-second deadline and no automatic retries. The caller may
// impose a shorter overall deadline through ctx. Normal SDK options preserve
// the caller's authentication, headers, endpoint, and transport.
//
// Authentication rejection and rate limiting stop new requests; requests already
// in flight may complete. Entries prevented by that stop remain unknown. Results
// retain catalog order regardless of completion order.
func Discover(ctx context.Context, service *openai.ModelService, includeSnapshots bool, opts ...option.RequestOption) []Result {
	return discover(ctx, service, Catalog(includeSnapshots), time.Now(), 5*time.Second, opts...)
}

func discover(ctx context.Context, service *openai.ModelService, entries []Entry, now time.Time, timeout time.Duration, opts ...option.RequestOption) []Result {
	results := make([]Result, len(entries))
	for i, entry := range entries {
		results[i].Entry = entry
	}
	requestOptions := append(append([]option.RequestOption(nil), opts...), option.WithMaxRetries(0))
	var mu sync.Mutex
	var workers sync.WaitGroup
	next := 0
	var stopped Failure
	for range min(3, len(entries)) {
		workers.Go(func() {
			for {
				mu.Lock()
				if next == len(entries) || stopped != "" || ctx.Err() != nil {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()

				requestContext, cancel := context.WithTimeout(ctx, timeout)
				model, err := service.Get(requestContext, entries[i].ID, requestOptions...)
				cancel()
				results[i] = classify(entries[i], model, err, now)
				if results[i].Failure == FailureAuthentication || results[i].Failure == FailureRateLimit {
					mu.Lock()
					if stopped == "" {
						stopped = results[i].Failure
					}
					mu.Unlock()
				}
			}
		})
	}
	workers.Wait()
	for i := range results {
		if results[i].Status == "" {
			results[i].Status = StatusUnknown
			results[i].Failure = stopped
			if stopped == "" {
				results[i].Failure = classifyFailure(ctx.Err())
			}
		}
	}
	return results
}

func classify(entry Entry, model *openai.Model, err error, now time.Time) Result {
	result := Result{Entry: entry, Status: StatusUnknown}
	if err != nil {
		var apiError *openai.Error
		if errors.As(err, &apiError) && apiError.StatusCode == http.StatusNotFound {
			result.Status = StatusNotVisible
			return result
		}
		result.Failure = classifyFailure(err)
		return result
	}
	if model == nil || model.ID != entry.ID {
		result.Failure = FailureInvalidResponse
		return result
	}
	// Retain support for metadata without a discriminator. When supplied, it must
	// identify model metadata; a matching ID alone cannot validate another object.
	if model.JSON.Object.Raw() != "" && (!model.JSON.Object.Valid() || model.Object != "model") {
		result.Failure = FailureInvalidResponse
		return result
	}
	dateRaw := model.JSON.ShutdownDate.Raw()
	if dateRaw != "" && dateRaw != "null" && !model.JSON.ShutdownDate.Valid() {
		result.Failure = FailureInvalidResponse
		return result
	}
	result.Status = StatusVisible
	if !model.ShutdownDate.IsZero() {
		result.ShutdownDate = model.ShutdownDate.UTC().Format(time.DateOnly)
		if result.ShutdownDate <= now.UTC().Format(time.DateOnly) {
			result.Status = StatusRetired
		}
	}
	return result
}

func classifyFailure(err error) Failure {
	if errors.Is(err, context.Canceled) {
		return FailureCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout
	}
	var apiError *openai.Error
	if errors.As(err, &apiError) {
		switch apiError.StatusCode {
		case http.StatusUnauthorized:
			return FailureAuthentication
		case http.StatusForbidden:
			return FailureForbidden
		case http.StatusTooManyRequests:
			return FailureRateLimit
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return FailureTimeout
		default:
			if apiError.StatusCode >= 500 {
				return FailureServer
			}
			return FailureRequest
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return FailureTimeout
		}
		return FailureNetwork
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return FailureInvalidResponse
	}
	return FailureRequest
}
