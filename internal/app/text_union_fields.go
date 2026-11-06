package app

// Older generated commands do not declare which union operands accept free
// text. Limit the compatibility registrations to verified request-body fields.
// Keys are canonical resource/action pairs; values are API field paths, not
// CLI aliases. Generated per-operand policies replace this table as a unit.
var legacyTextUnionFields = map[string][]string{
	"responses create":                  {"input", "conversation", "instructions", "previous_response_id"},
	"beta:responses create":             {"input", "conversation", "instructions", "previous_response_id"},
	"responses:input-tokens count":      {"input", "conversation", "instructions", "previous_response_id"},
	"beta:responses:input-tokens count": {"input", "conversation", "instructions", "previous_response_id"},
	"responses compact":                 {"input", "instructions", "previous_response_id"},
	"beta:responses compact":            {"input", "instructions", "previous_response_id"},
	"chat:completions create":           {"stop"},
	"completions create":                {"prompt", "stop"},
	"embeddings create":                 {"input"},
	"vector-stores search":              {"query"},
	"audio:speech create":               {"voice"},
	"beta:threads:runs create":          {"instructions", "additional_messages.content"},
}
