package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type webhookTestRequest struct {
	project, endpoint, event string
}

// Capture the submitted request after flag, stdin, file and header resolution.
// The webhook JSON body uses WithRequestBody([]byte), whose SDK GetBody returns
// an independent reader. Never read stdin or the outgoing Body for presentation.
func CaptureTestRequest(request *http.Request, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	command, ok := request.Context().Value(webhookCommandKey{}).(webhookCommandContext)
	if !ok || command.operation != "test" || command.request == nil {
		return next(request)
	}
	value := &webhookTestRequest{project: request.Header.Get("OpenAI-Project")}
	if request.URL != nil {
		path, test := strings.CutSuffix(request.URL.EscapedPath(), "/test")
		if test {
			if boundary := strings.LastIndex(path, "/webhook_endpoints/"); boundary >= 0 {
				endpoint := path[boundary+len("/webhook_endpoints/"):]
				if !strings.Contains(endpoint, "/") {
					value.endpoint, _ = url.PathUnescape(endpoint)
				}
			}
		}
	}
	if request.GetBody != nil {
		if body, err := request.GetBody(); err == nil && body != nil {
			var params map[string]json.RawMessage
			if json.NewDecoder(webhookRequestReader{request, body}).Decode(&params) == nil {
				// JSON field names are case-sensitive. Do not treat EVENT_TYPE
				// as the canonical field or let it mask an explicit null.
				var event string
				if json.Unmarshal(params["event_type"], &event) == nil {
					value.event = event
				}
			}
			body.Close()
		}
	}
	command.request.Store(value)
	return next(request)
}

type webhookRequestReader struct {
	request *http.Request
	body    io.Reader
}

func (reader webhookRequestReader) Read(data []byte) (int, error) {
	if err := reader.request.Context().Err(); err != nil {
		return 0, err
	}
	// Bound each copy so large buffered inputs keep checking cancellation.
	return reader.body.Read(data[:min(len(data), 32<<10)])
}

func (command webhookCommandContext) testRequest() webhookTestRequest {
	if command.request != nil {
		if value := command.request.Load(); value != nil {
			return *value
		}
	}
	return webhookTestRequest{}
}

func (request webhookTestRequest) summary() string {
	project := "determined by API key (ID unavailable)"
	if request.project != "" {
		project = webhookContextValue(request.project)
	}
	return "Project: " + project + "\nEndpoint: " + webhookContextValue(request.endpoint) +
		"\nEvent: " + webhookContextValue(request.event) + "\n\n"
}

func webhookContextValue(value string) string {
	if value == "" {
		return "(unavailable)"
	}
	if webhookCommandValue(value) {
		return value
	}
	return strconv.Quote(value)
}
