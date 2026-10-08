package custom

import (
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Agent and session creation can start durable work without a deduplication guarantee.
// Preserve the first outcome instead of silently submitting the operation again.
func agentsRequestOptions(command *cli.Command) []option.RequestOption {
	if command.Name != "create" {
		return nil
	}
	switch commandResourceName(command) {
	case "beta:agents", "beta:agents:sessions":
		return []option.RequestOption{option.WithMaxRetries(0)}
	}
	return nil
}

// agentsResolvedRequestOptions runs after JSON and header file inputs are resolved.
// Only keyed message-only batches have the documented safe-retry guarantee.
func agentsResolvedRequestOptions(command *cli.Command, parameterHeaders map[string]any, headers http.Header, body []byte) []option.RequestOption {
	if command.Name != "create" {
		return nil
	}
	switch commandResourceName(command) {
	case "beta:agents:sessions":
		return agentsSessionCreationRequestOptions(body)
	case "beta:agents:sessions:events":
	default:
		return nil
	}
	key := ""
	// Match SDK environment defaults; explicit request parameters follow them.
	for line := range strings.SplitSeq(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok && http.CanonicalHeaderKey(strings.TrimSpace(name)) == "Idempotency-Key" {
			key = strings.TrimSpace(value)
		}
	}
	for name, value := range parameterHeaders {
		if http.CanonicalHeaderKey(name) == "Idempotency-Key" {
			// The generated parameter is a scalar string. Null or unfamiliar
			// representations cannot establish a supported effective key.
			key, _ = value.(string)
		}
	}
	if values, present := headers[http.CanonicalHeaderKey("Idempotency-Key")]; present {
		key = ""
		if len(values) == 1 {
			key = values[0]
		}
	}
	// net/http trims surrounding space and tab characters when writing headers.
	key = strings.Trim(key, " \t")
	if supportedAgentsEventKey(key) && agentsMessageOnlyBody(body) {
		return nil
	}
	return []option.RequestOption{option.WithMaxRetries(0)}
}

func supportedAgentsEventKey(key string) bool {
	if len(key) == 0 || len(key) > 256 {
		return false
	}
	for i := range len(key) {
		if key[i] >= 0x7f || key[i] < 0x20 && key[i] != '\t' {
			return false
		}
	}
	return true
}

func agentsMessageOnlyBody(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	// Project only keys, counts, and types. Reading the events array itself
	// through GetBytes would copy prompts and images that this check never uses.
	eventKeys := 0
	gjson.GetBytes(body, "@keys").ForEach(func(_, key gjson.Result) bool {
		if key.Type == gjson.String && key.Str == "events" {
			eventKeys++
		}
		return true
	})
	count := gjson.GetBytes(body, "events.#")
	if eventKeys != 1 || count.Type != gjson.Number || count.Int() == 0 {
		return false
	}
	seen, messages := int64(0), true
	gjson.GetBytes(body, "events.#.type").ForEach(func(_, kind gjson.Result) bool {
		seen++
		messages = kind.Type == gjson.String && kind.Str == "agent.session.input.message"
		return messages
	})
	if !messages || seen != count.Int() {
		return false
	}
	seen = 0
	gjson.GetBytes(body, "events.#.@keys").ForEach(func(_, keys gjson.Result) bool {
		types := 0
		keys.ForEach(func(_, key gjson.Result) bool {
			if key.Type == gjson.String && key.Str == "type" {
				types++
			}
			return true
		})
		seen++
		messages = types == 1
		return messages
	})
	return messages && seen == count.Int()
}
