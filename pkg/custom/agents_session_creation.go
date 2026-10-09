package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

type agentsSessionCreationKey struct{}
type agentsSessionCreationIntent struct{ withoutInput atomic.Bool }

// Wrap the canonical action before subgroup cloning. Each invocation owns its cell.
func configureAgentsSessionCreation(root *cli.Command) {
	resource := root.Command("beta:agents:sessions")
	if resource == nil {
		return
	}
	command := resource.Command("create")
	if command == nil || command.Action == nil {
		return
	}
	previous := command.Action
	command.Action = func(ctx context.Context, command *cli.Command) error {
		return previous(context.WithValue(ctx, agentsSessionCreationKey{}, &agentsSessionCreationIntent{}), command)
	}
}

func agentsSessionCreationWithoutInput(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	intent, _ := ctx.Value(agentsSessionCreationKey{}).(*agentsSessionCreationIntent)
	return intent != nil && intent.withoutInput.Load()
}

// Capture only the final intent, never body bytes. SDK execution invokes this
// middleware synchronously before the generated action starts stream presentation.
func agentsSessionCreationRequestOptions(body []byte) []option.RequestOption {
	withoutInput := agentsSelfHostedCreationWithoutInput(body)
	return []option.RequestOption{option.WithMiddleware(func(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if intent, _ := request.Context().Value(agentsSessionCreationKey{}).(*agentsSessionCreationIntent); intent != nil {
			intent.withoutInput.Store(withoutInput)
		}
		return next(request)
	})}
}

type agentsInputPresence struct {
	count uint8
	null  bool
}

func (p *agentsInputPresence) UnmarshalJSON(value []byte) error {
	if p.count < 2 {
		p.count++
	}
	p.null = bytes.Equal(bytes.TrimSpace(value), []byte("null"))
	return nil
}

type agentsCreationEnvironmentType struct {
	count      uint8
	selfHosted bool
}

func (p *agentsCreationEnvironmentType) UnmarshalJSON(value []byte) error {
	if p.count < 2 {
		p.count++
	}
	value = bytes.TrimSpace(value)
	// Every spelling of this fixed ASCII enum fits this bound, including escapes.
	// Longer values remain strict; this does not reject or change request bytes.
	if len(value) <= 2+6*len("self_hosted") {
		var kind string
		p.selfHosted = json.Unmarshal(value, &kind) == nil && kind == "self_hosted"
	}
	return nil
}

type agentsCreationEnvironment struct {
	count uint8
	kind  agentsCreationEnvironmentType
	valid bool
}

func (e *agentsCreationEnvironment) UnmarshalJSON(value []byte) error {
	if e.count < 2 {
		e.count++
	}
	var fields struct {
		Type agentsCreationEnvironmentType `json:"type"`
	}
	e.valid = json.Unmarshal(value, &fields) == nil && agentsCanonicalIntentKeys(value, "type")
	e.kind = fields.Type
	return nil
}

// The decoder skips values without copying them. Only object keys need storage.
type agentsIgnoredIntentValue struct{}

func (*agentsIgnoredIntentValue) UnmarshalJSON([]byte) error { return nil }

// encoding/json accepts case-folded struct keys. Ambiguous spellings cannot
// establish this exception, even though request serialization stays unchanged.
func agentsCanonicalIntentKeys(body []byte, relevant ...string) bool {
	var fields map[string]agentsIgnoredIntentValue
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return false
	}
	for key := range fields {
		for _, expected := range relevant {
			if strings.EqualFold(key, expected) && key != expected {
				return false
			}
		}
	}
	return true
}

func agentsSelfHostedCreationWithoutInput(body []byte) bool {
	var fields struct {
		Input       agentsInputPresence       `json:"input"`
		Environment agentsCreationEnvironment `json:"environment"`
	}
	if json.Unmarshal(body, &fields) != nil || !agentsCanonicalIntentKeys(body, "input", "environment") {
		return false
	}
	environment := fields.Environment
	return environment.count == 1 && environment.valid && environment.kind.count == 1 && environment.kind.selfHosted &&
		(fields.Input.count == 0 || fields.Input.count == 1 && fields.Input.null)
}
