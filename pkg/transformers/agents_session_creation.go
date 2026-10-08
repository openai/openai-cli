package transformers

import (
	"strings"

	"github.com/tidwall/gjson"
)

type agentsCreationAcknowledgement struct {
	acknowledged, invalid, work, inconsistent bool
	hasSession, hasEnvironment                bool
	session, environment                      [32]byte
}

func (s *AgentsStreamState) acceptsNoInputCreation(route Route) bool {
	return s.AllowNoInputSessionCreation && route.OutputKind == OutputStreamEvent &&
		route.Operation == "(resource) beta.agents.sessions > (method) create"
}

func (s *AgentsStreamState) observeSessionCreation(value gjson.Result, route Route) {
	if !s.acceptsNoInputCreation(route) {
		return
	}
	creation := &s.creation
	if !agentsUniqueFields(value, "type", "session_id", "session") {
		creation.invalid = true
	}
	if outer := value.Get("session_id"); outer.Exists() {
		creation.recordSession(outer)
	}
	kind := value.Get("type").Str
	if strings.HasPrefix(kind, "agent.session.environment.") {
		if !agentsUniqueFields(value, "environment", "environment_id", "turn_id") {
			creation.invalid = true
		}
		if environment := value.Get("environment"); environment.Exists() {
			creation.recordEnvironment(environment)
		}
		if id := value.Get("environment_id"); id.Exists() {
			creation.recordEnvironmentID(id)
		}
		if turn := value.Get("turn_id"); turn.Exists() && turn.Raw != "null" {
			creation.work = true
		}
	}
	if strings.HasPrefix(kind, "agent.session.turn.") || strings.HasPrefix(kind, "agent.session.subagent.") || strings.HasPrefix(kind, "agent.session.input.") ||
		kind == "agent.output.command_execution_output.delta" || kind == "agent.session.in_progress" || kind == "agent.session.requires_action" {
		creation.work = true
	}
	switch kind {
	case "agent.session.created", "agent.session.idle", "agent.session.in_progress", "agent.session.requires_action", "agent.session.failed":
	default:
		return
	}
	session := value.Get("session")
	if session.IsObject() {
		if !agentsUniqueFields(session, "id", "status", "error", "environment", "required_actions") {
			creation.invalid = true
		}
		if environment := session.Get("environment"); environment.Exists() {
			creation.recordEnvironment(environment)
		}
		if id := session.Get("id"); id.Exists() {
			creation.recordSession(id)
		}
		if status := session.Get("status"); status.Exists() && status.Str != "idle" {
			creation.work = true
		}
		if session.Get("status").Str == "failed" || session.Get("error").Exists() && session.Get("error").Raw != "null" {
			creation.invalid = true
		}
		if actions := session.Get("required_actions"); actions.Exists() && actions.Raw != "null" &&
			(!actions.IsArray() || actions.Get("#").Int() != 0) {
			creation.work = true
		}
	}
	if kind != "agent.session.created" {
		return
	}
	environment := session.Get("environment")
	id, environmentID := session.Get("id"), environment.Get("id")
	valid := session.IsObject() && agentsUniqueFields(session, "id", "status", "error", "environment", "required_actions") &&
		id.Type == gjson.String && id.Str != "" && session.Get("status").Type == gjson.String && session.Get("status").Str == "idle" &&
		(!session.Get("error").Exists() || session.Get("error").Raw == "null") &&
		environment.IsObject() && agentsUniqueFields(environment, "id", "type") &&
		environment.Get("type").Type == gjson.String && environment.Get("type").Str == "self_hosted" &&
		environmentID.Type == gjson.String && environmentID.Str != ""
	if !valid {
		creation.invalid = true
		return
	}
	creation.acknowledged = true
}

func (s *agentsCreationAcknowledgement) recordSession(id gjson.Result) {
	if id.Type != gjson.String || id.Str == "" {
		s.invalid = true
		return
	}
	key := agentsIdentity(id.Str)
	if s.hasSession && s.session != key {
		s.inconsistent = true
	}
	s.session, s.hasSession = key, true
}

func (s *agentsCreationAcknowledgement) recordEnvironment(environment gjson.Result) {
	if !agentsUniqueFields(environment, "id", "type", "status", "error") {
		s.invalid = true
		return
	}
	if kind := environment.Get("type"); kind.Exists() && (kind.Type != gjson.String || kind.Str != "self_hosted") {
		s.inconsistent = true
	}
	if environment.Get("status").Str == "failed" || environment.Get("error").Exists() && environment.Get("error").Raw != "null" {
		s.invalid = true
	}
	if id := environment.Get("id"); id.Exists() {
		s.recordEnvironmentID(id)
	}
}

func (s *agentsCreationAcknowledgement) recordEnvironmentID(id gjson.Result) {
	if id.Type != gjson.String || id.Str == "" {
		s.invalid = true
		return
	}
	key := agentsIdentity(id.Str)
	if s.hasEnvironment && s.environment != key {
		s.inconsistent = true
	}
	s.environment, s.hasEnvironment = key, true
}

// Repeated or case-ambiguous semantic fields cannot prove an outcome.
func agentsUniqueFields(value gjson.Result, fields ...string) bool {
	if !value.IsObject() {
		return false
	}
	seen, unique := uint64(0), true
	value.ForEach(func(key, _ gjson.Result) bool {
		for i, field := range fields {
			if strings.EqualFold(key.Str, field) {
				bit := uint64(1) << i
				if key.Str != field || seen&bit != 0 {
					unique = false
					return false
				}
				seen |= bit
			}
		}
		return true
	})
	return unique
}

func (s *AgentsStreamState) hasNoInputCreationAcknowledgement(route Route) bool {
	return s.acceptsNoInputCreation(route) && s.creation.acknowledged &&
		!s.creation.invalid && !s.creation.work && !s.creation.inconsistent
}
