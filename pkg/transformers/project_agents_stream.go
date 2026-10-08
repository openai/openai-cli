package transformers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

const agentsTrackedParts = 256
const agentsRecentEvents = 256

type agentsTextState struct {
	key   [32]byte
	final bool
}

// AgentsStreamProjector bounds human-only identity tracking. Once part tracking
// fills, new parts remain readable raw events. Machine output never calls it.
type AgentsStreamProjector struct {
	parts      [agentsTrackedParts]agentsTextState
	partCount  int
	recent     [agentsRecentEvents][32]byte
	recentSize int
	recentNext int
	omitted    bool
}

// HasOmissions reports whether projection omitted known fields from any event.
// Presentation owns diagnostic wording, destination, and suppression policy.
func (s *AgentsStreamProjector) HasOmissions() bool { return s.omitted }

func (s *AgentsStreamProjector) duplicate(value gjson.Result) bool {
	id := value.Get("event_id")
	if id.Type != gjson.String || id.Str == "" {
		return false
	}
	// The complete payload distinguishes changed data reusing an event ID.
	digest := sha256.Sum256([]byte(value.Raw))
	for i := 0; i < s.recentSize; i++ {
		if s.recent[i] == digest {
			return true
		}
	}
	s.recent[s.recentNext] = digest
	s.recentNext = (s.recentNext + 1) % len(s.recent)
	if s.recentSize < len(s.recent) {
		s.recentSize++
	}
	return false
}

func (s *AgentsStreamProjector) textPart(value, text, item gjson.Result, content, label string, snapshot, final bool) (readable.StreamPart, bool) {
	session, turn := value.Get("session_id"), value.Get("turn_id")
	if text.Type != gjson.String || session.Type != gjson.String || session.Str == "" ||
		turn.Type != gjson.String || turn.Str == "" || item.Type != gjson.String || item.Str == "" {
		return readable.StreamPart{}, false
	}
	key := agentsIdentity(session.Str, turn.Str, item.Str, content, label)
	index := -1
	for i := 0; i < s.partCount; i++ {
		if s.parts[i].key == key {
			index = i
			break
		}
	}
	if index < 0 {
		if s.partCount == len(s.parts) {
			return readable.StreamPart{}, false
		}
		index = s.partCount
		s.parts[index].key = key
		s.partCount++
	}
	if s.parts[index].final && !final {
		return readable.StreamPart{}, true
	}
	s.parts[index].final = s.parts[index].final || final
	return readable.StreamPart{Key: "agents:" + hex.EncodeToString(key[:]), Text: text.Str, Label: label, Snapshot: snapshot}, true
}

// Project selects text and lifecycle details for the existing StreamWriter.
// Unknown events and unsupported shapes return false for complete rendering.
func (s *AgentsStreamProjector) Project(ctx context.Context, value gjson.Result, route Route) (readable.StreamEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return readable.StreamEvent{}, false, err
	}
	if !IsAgentsStream(route) || !value.IsObject() || !gjson.Valid(value.Raw) {
		return readable.StreamEvent{}, false, nil
	}
	kind := value.Get("type").String()
	var event readable.StreamEvent
	var projected bool
	var hidden bool
	var err error
	var projectedState *AgentsStreamProjector
	switch kind {
	case "agent.session.turn.reasoning_summary_text.delta", "agent.session.turn.reasoning_summary_text.done",
		"agent.session.turn.reasoning_summary_part.added", "agent.session.turn.reasoning_summary_part.done":
		pending := *s
		event, projected = pending.reasoning(ctx, value, kind)
		projectedState = &pending
	case "agent.session.turn.output_text.delta", "agent.session.turn.output_text.done":
		field, final := "delta", strings.HasSuffix(kind, ".done")
		if final {
			field = "text"
		}
		if !agentsUniqueFields(value, "type", "event_id", "session_id", "turn_id", "item_id", "output_index", "content_index", field) ||
			!agentsTextMetadataValid(value) || !value.Get("content_index").Exists() {
			break
		}
		content, ok := streamIndex(value.Get("content_index"))
		if !ok {
			break
		}
		var part readable.StreamPart
		part, projected = s.textPart(value, value.Get(field), value.Get("item_id"), content, "Agent", final, final)
		if projected {
			if s.duplicate(value) {
				return readable.StreamEvent{}, true, ctx.Err()
			}
			if part.Key != "" {
				event.Parts = []readable.StreamPart{part}
			}
			event.Details = streamResidual(value, nil, "type", "event_id", "session_id", "turn_id", "item_id", "output_index", "content_index", field)
		}
	case "agent.session.turn.content_part.added", "agent.session.turn.content_part.done":
		if !agentsUniqueFields(value, "type", "event_id", "session_id", "turn_id", "item_id", "output_index", "content_index", "part") ||
			!agentsTextMetadataValid(value) || !value.Get("content_index").Exists() {
			break
		}
		part := value.Get("part")
		content, ok := streamIndex(value.Get("content_index"))
		if !ok || !agentsUniqueFields(part, "type", "text") || part.Get("type").String() != "output_text" {
			break
		}
		final := strings.HasSuffix(kind, ".done")
		text, ok := s.textPart(value, part.Get("text"), value.Get("item_id"), content, "Agent", true, final)
		if !ok {
			break
		}
		if text.Key != "" {
			event.Parts = []readable.StreamPart{text}
		}
		remaining := streamResidual(part, nil, "type", "text")
		event.Details = streamResidual(value, map[string]gjson.Result{"part": remaining},
			"type", "event_id", "session_id", "turn_id", "item_id", "output_index", "content_index")
		projected = true
	case "agent.session.created", "agent.session.idle", "agent.session.in_progress", "agent.session.requires_action", "agent.session.failed":
		event, projected, hidden, err = summarizeAgentsEvent(ctx, value, "session",
			"id status error required_actions", "object created_at last_active_at agent environment metadata usage vault_ids")
	case "agent.session.turn.created", "agent.session.turn.in_progress", "agent.session.turn.completed",
		"agent.session.turn.failed", "agent.session.turn.cancelled":
		event, projected, hidden, err = summarizeAgentsEvent(ctx, value, "turn",
			"id session_id status subagent_id error usage", "object agent_id created_at started_at completed_at")
	case "agent.session.environment.failed",
		"agent.session.environment.ready", "agent.session.environment.pending", "agent.session.environment.connected",
		"agent.session.environment.disconnected":
		event, projected, hidden, err = summarizeAgentsEvent(ctx, value, "environment", "id type status error", "")
	case "agent.session.subagent.created", "agent.session.subagent.active", "agent.session.subagent.closed":
		event, projected, hidden, err = summarizeAgentsEvent(ctx, value, "subagent",
			"id session_id name status parent_agent_id", "object opened_at closed_at instructions")
	case "agent.session.environment.reset", "error":
		event.Details = streamResidual(value, nil, "event_id")
		projected = true
	case "agent.output.command_execution_output.delta":
		// Command items carry activity and exit status. Raw mode retains every
		// output delta; human output avoids printing the entire tool transcript.
		var summary gjson.Result
		summary, hidden, err = summarizeResourceFields(ctx, value, nil,
			strings.Fields("type event_id session_id turn_id item_id output_index delta"))
		projected = summary.Exists()
	case "agent.session.turn.item.added", "agent.session.turn.item.done":
		event, projected, hidden, err = summarizeAgentsToolEvent(ctx, value)
		if projected || err != nil {
			break
		}
		item := value.Get("item")
		if !item.IsObject() {
			break
		}
		if item.Get("type").String() == "message" && item.Get("role").String() == "assistant" {
			// A failed multipart projection must not finalize earlier parts. Copy
			// only bounded projector state, never the event or its text payloads.
			pending := *s
			event, projected = pending.message(ctx, value, item, kind == "agent.session.turn.item.done")
			projectedState = &pending
			break
		}
	}
	if err != nil {
		return readable.StreamEvent{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return readable.StreamEvent{}, false, err
	}
	if projected && projectedState != nil {
		*s = *projectedState
	}
	s.omitted = s.omitted || projected && hidden
	return event, projected, nil
}

func (s *AgentsStreamProjector) message(ctx context.Context, value, item gjson.Result, final bool) (readable.StreamEvent, bool) {
	if !agentsMessageSnapshotValid(ctx, value, item) {
		return readable.StreamEvent{}, false
	}
	content := item.Get("content")
	var event readable.StreamEvent
	var residual strings.Builder
	residual.WriteByte('[')
	index, hasResidual, valid := 0, false, true
	content.ForEach(func(_, part gjson.Result) bool {
		if ctx.Err() != nil {
			valid = false
			return false
		}
		remaining := part
		if part.Get("type").String() == "output_text" {
			projected, ok := s.textPart(value, part.Get("text"), item.Get("id"), strconv.Itoa(index), "Agent", true, final)
			if !ok {
				valid = false
				return false
			}
			if projected.Key != "" {
				event.Parts = append(event.Parts, projected)
			}
			remaining = streamResidual(part, nil, "type", "text")
		}
		if index > 0 {
			residual.WriteByte(',')
		}
		if remaining.Exists() {
			hasResidual = true
			residual.WriteString(remaining.Raw)
		} else {
			residual.WriteString("null")
		}
		index++
		return true
	})
	if !valid {
		return readable.StreamEvent{}, false
	}
	residual.WriteByte(']')
	remaining := gjson.Result{}
	if hasResidual {
		remaining = gjson.Parse(residual.String())
	}
	replacements := map[string]gjson.Result{"content": remaining}
	if status := item.Get("status"); status.Type == gjson.String && (status.Str == "completed" || status.Str == "in_progress") {
		replacements["status"] = gjson.Result{}
	}
	itemDetails := streamResidual(item, replacements, "id", "type", "role")
	event.Details = streamResidual(value, map[string]gjson.Result{"item": itemDetails},
		"type", "event_id", "session_id", "turn_id", "output_index")
	return event, true
}

// Metadata removed from a text event must have an understood shape. Identity
// strings and text values receive their remaining checks in textPart.
func agentsTextMetadataValid(value gjson.Result) bool {
	if id := value.Get("event_id"); id.Exists() && id.Type != gjson.String {
		return false
	}
	output := value.Get("output_index")
	// Added history/input items permit null here. This index is not a text key.
	if output.Type == gjson.Null && output.Raw == "null" && value.Get("type").Str == "agent.session.turn.item.added" {
		return true
	}
	_, valid := streamIndex(output)
	return valid
}

func agentsMessageSnapshotValid(ctx context.Context, value, item gjson.Result) bool {
	if !agentsUniqueFields(value, "type", "event_id", "session_id", "turn_id", "output_index", "item") ||
		!agentsUniqueFields(item, "id", "type", "role", "status", "content", "turn_id") || !agentsTextMetadataValid(value) {
		return false
	}
	for _, id := range []gjson.Result{value.Get("session_id"), value.Get("turn_id"), item.Get("id")} {
		if id.Type != gjson.String || id.Str == "" {
			return false
		}
	}
	if turn := item.Get("turn_id"); turn.Exists() && (turn.Type != gjson.String || turn.Str != value.Get("turn_id").Str) {
		return false
	}
	content := item.Get("content")
	if !content.IsArray() {
		return false
	}
	valid := true
	content.ForEach(func(_, part gjson.Result) bool {
		if ctx.Err() != nil {
			valid = false
			return false
		}
		if part.IsObject() {
			valid = agentsUniqueFields(part, "type")
			if part.Get("type").Str == "output_text" {
				valid = valid && agentsUniqueFields(part, "text") && part.Get("text").Type == gjson.String
			}
		}
		return valid
	})
	return valid && ctx.Err() == nil
}
