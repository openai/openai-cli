package transformers

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"

	"github.com/tidwall/gjson"
)

type agentsTurnState struct {
	terminal bool
	known    bool
}

// AgentsStreamState tracks root outcomes without retaining event bodies.
// Memory scales with distinct turn identities. Digest-only tombstones preserve
// terminal outcomes and subagent attribution across late, out-of-order events.
type AgentsStreamState struct {
	// Enable only for finalized self-hosted creation with omitted or null input.
	AllowNoInputSessionCreation bool
	creation                    agentsCreationAcknowledgement
	turns                       map[[32]byte]agentsTurnState
	subagentTurns               map[[32]byte]struct{}
	completed                   bool
	inconsistent                bool
	failure                     string
}

// IsAgentsStream limits presentation and lifecycle rules to generated Agents routes.
func IsAgentsStream(route Route) bool {
	return route.OutputKind == OutputStreamEvent &&
		(route.Operation == "(resource) beta.agents.sessions > (method) create" ||
			route.Operation == "(resource) beta.agents.sessions.events > (method) stream")
}

func agentsRootTurn(value gjson.Result) (string, string, bool) {
	session, turn, root, known := agentsTurnRole(value)
	return session, turn, root && known
}

func agentsTurnRole(value gjson.Result) (string, string, bool, bool) {
	if !value.IsObject() || !value.Get("turn").IsObject() {
		return "", "", false, false
	}
	session, turn := value.Get("session_id"), value.Get("turn_id")
	subagent := value.Get("turn.subagent_id")
	root := subagent.Raw == "null" || subagent.Type == gjson.String && subagent.Str == ""
	known := root || subagent.Type == gjson.String && subagent.Str != ""
	if !known || session.Type != gjson.String || session.Str == "" || turn.Type != gjson.String || turn.Str == "" {
		return "", "", false, false
	}
	if embedded := value.Get("turn.id"); embedded.Exists() && (embedded.Type != gjson.String || embedded.Str != turn.Str) {
		return "", "", false, false
	}
	if embedded := value.Get("turn.session_id"); embedded.Exists() && (embedded.Type != gjson.String || embedded.Str != session.Str) {
		return "", "", false, false
	}
	return session.Str, turn.Str, root, true
}

// AgentsRootTurnTerminal excludes subagent and malformed terminal events.
// It classifies outcomes only. Trailing stream events must remain visible.
func AgentsRootTurnTerminal(value gjson.Result, route Route) bool {
	if !IsAgentsStream(route) {
		return false
	}
	_, _, root := agentsRootTurn(value)
	if !root {
		return false
	}
	status := strings.TrimPrefix(value.Get("type").String(), "agent.session.turn.")
	if status != "completed" && status != "failed" && status != "cancelled" {
		return false
	}
	embedded := value.Get("turn.status")
	return !embedded.Exists() || embedded.Type == gjson.String && embedded.Str == status
}

// AgentsStreamFailure classifies lifecycle failures without exposing API prose.
// A failed tool or subagent remains data, not a failed root turn.
func AgentsStreamFailure(value gjson.Result, route Route) string {
	if !IsAgentsStream(route) || !value.IsObject() {
		return ""
	}
	switch value.Get("type").String() {
	case "error":
		return "the Agents API reported an error while streaming"
	case "agent.session.failed":
		return "the agent session failed"
	case "agent.session.environment.failed":
		return "the agent environment failed"
	case "agent.session.turn.failed":
		if AgentsRootTurnTerminal(value, route) {
			return "the agent turn failed"
		}
	case "agent.session.turn.cancelled":
		if AgentsRootTurnTerminal(value, route) {
			return "the agent turn was cancelled"
		}
	}
	return ""
}

func (s *AgentsStreamState) Observe(value gjson.Result, route Route) {
	if !IsAgentsStream(route) {
		return
	}
	if !gjson.Valid(value.Raw) {
		if s.acceptsNoInputCreation(route) {
			s.creation.invalid = true
		}
		return
	}
	s.observeSessionCreation(value, route)
	if failure := AgentsStreamFailure(value, route); failure != "" && s.failure == "" {
		s.failure = failure
	}
	session, turn := value.Get("session_id"), value.Get("turn_id")
	if session.Type != gjson.String || session.Str == "" || turn.Type != gjson.String || turn.Str == "" {
		return
	}
	kind := value.Get("type").String()
	if !strings.HasPrefix(kind, "agent.session.turn.") && kind != "agent.output.command_execution_output.delta" {
		return
	}
	_, _, root, known := agentsTurnRole(value)
	key := agentsIdentity(session.Str, turn.Str)
	if known && !root {
		if previous := s.turns[key]; previous.known {
			s.inconsistent = true
		}
		delete(s.turns, key)
		if s.subagentTurns == nil {
			s.subagentTurns = make(map[[32]byte]struct{})
		}
		s.subagentTurns[key] = struct{}{}
		return
	}
	if _, subagent := s.subagentTurns[key]; subagent {
		if root {
			s.inconsistent = true
		}
		return
	}
	status := strings.TrimPrefix(kind, "agent.session.turn.")
	terminal := known && (status == "completed" || status == "failed" || status == "cancelled")
	if embedded := value.Get("turn.status"); embedded.Exists() && (embedded.Type != gjson.String || embedded.Str != status) {
		terminal = false
	}
	if s.turns == nil {
		s.turns = make(map[[32]byte]agentsTurnState)
	}
	previous := s.turns[key]
	s.turns[key] = agentsTurnState{terminal: previous.terminal || terminal, known: previous.known || known}
	s.completed = s.completed || terminal && root
}

func agentsIdentity(parts ...string) [32]byte {
	digest := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(part))
	}
	var key [32]byte
	copy(key[:], digest.Sum(nil))
	return key
}

// CompletionError applies only when the source reaches EOF, not a local item limit.
func (s *AgentsStreamState) CompletionError(route Route) string {
	if !IsAgentsStream(route) {
		return ""
	}
	if s.failure != "" {
		return s.failure
	}
	if s.acceptsNoInputCreation(route) && s.creation.inconsistent {
		return "the stream reported inconsistent agent session identities"
	}
	if s.inconsistent {
		return "the stream reported inconsistent agent turn identities"
	}
	for _, turn := range s.turns {
		if !turn.terminal {
			return "the stream ended before the agent turn completed"
		}
	}
	if !s.completed && !s.hasNoInputCreationAcknowledgement(route) {
		return "the stream ended without a confirmed agent turn outcome"
	}
	return ""
}
