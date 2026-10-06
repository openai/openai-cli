package custom

import (
	"strings"

	"github.com/urfave/cli/v3"
)

// These summaries describe CLI workflows, including local image behavior, rather
// than API schema prose. Applying them after decoration keeps generated routes,
// request contracts, and future authored resource descriptions intact.
var commandGroupDescriptions = map[string]string{
	"responses":                               "Generate and manage model responses.",
	"responses input-items":                   "List the input items of a response.",
	"responses input-tokens":                  "Count input tokens for a response request.",
	"chat":                                    "Create and manage Chat Completions.",
	"chat completions":                        "Create and manage stored Chat Completions.",
	"chat completions messages":               "List messages in a stored Chat Completion.",
	"completions":                             "Generate text completions from prompts.",
	"images":                                  "Generate, edit, save, and preview images.",
	"audio":                                   "Transcribe audio, generate speech, and create voices.",
	"audio transcriptions":                    "Convert audio to text.",
	"audio translations":                      "Translate supported audio to English text.",
	"audio speech":                            "Generate spoken audio from text.",
	"audio voices":                            "Create voices from descriptions or audio samples.",
	"videos":                                  "Generate, edit, and download videos.",
	"live":                                    "Handle Live calls and stored sessions.",
	"live sessions":                           "Accept and manage Live sessions and recordings.",
	"realtime":                                "Create client secrets and manage Realtime calls.",
	"realtime client-secrets":                 "Create client secrets for Realtime sessions.",
	"realtime calls":                          "Accept and manage Realtime calls.",
	"files":                                   "Upload and manage API files.",
	"uploads":                                 "Assemble multipart file uploads.",
	"uploads parts":                           "Upload parts of a multipart file upload.",
	"vector-stores":                           "Index files and search their contents.",
	"vector-stores files":                     "Attach and manage files in a vector store.",
	"vector-stores file-batches":              "Add and manage batches of vector store files.",
	"embeddings":                              "Create embedding vectors from text.",
	"conversations":                           "Manage stored conversations and their items.",
	"conversations items":                     "Create and manage items in a conversation.",
	"containers":                              "Manage API containers and their files.",
	"containers files":                        "Upload and manage files in a container.",
	"containers files content":                "Download a container file's contents.",
	"skills":                                  "Manage remote project skill bundles.",
	"skills content":                          "Download a remote skill bundle.",
	"skills versions":                         "Create and manage versions of a remote skill.",
	"skills versions content":                 "Download a version of a remote skill bundle.",
	"webhooks":                                "Manage webhook endpoints and event types.",
	"webhooks event-types":                    "List available webhook event types.",
	"models":                                  "List and inspect available models.",
	"fine-tuning":                             "Manage training jobs, checkpoints, and graders.",
	"fine-tuning jobs":                        "Create and manage fine-tuning jobs.",
	"fine-tuning jobs checkpoints":            "List checkpoints for a fine-tuning job.",
	"fine-tuning checkpoints":                 "Manage access to fine-tuned model checkpoints.",
	"fine-tuning checkpoints permissions":     "Manage permissions for a fine-tuned checkpoint.",
	"fine-tuning alpha":                       "Browse alpha fine-tuning operations.",
	"fine-tuning alpha graders":               "Run and validate fine-tuning graders.",
	"batches":                                 "Submit and monitor batches of API requests.",
	"moderations":                             "Classify potentially harmful text and image inputs.",
	"content-provenance-checks":               "Check supported OpenAI image and audio provenance signals.",
	"safety":                                  "Retrieve safety cases and project alerts.",
	"safety cases":                            "Retrieve details of a safety case.",
	"safety alerts":                           "Retrieve project safety alerts.",
	"beta":                                    "Browse beta Responses, ChatKit, Assistants, and Threads APIs.",
	"beta responses":                          "Create and manage responses through the beta API.",
	"beta responses input-items":              "List input items through the beta Responses API.",
	"beta responses input-tokens":             "Count input tokens through the beta Responses API.",
	"beta assistants":                         "Create and manage beta assistants.",
	"beta threads":                            "Create and manage beta assistant threads.",
	"beta threads runs":                       "Create and manage runs on a beta thread.",
	"beta threads runs steps":                 "Inspect the steps of a beta thread run.",
	"beta threads messages":                   "Create and manage messages in a beta thread.",
	"beta chatkit":                            "Manage ChatKit sessions and threads.",
	"beta chatkit sessions":                   "Create and manage ChatKit sessions.",
	"beta chatkit threads":                    "Inspect and manage ChatKit threads and items.",
	"admin":                                   "Manage organization access, projects, usage, and settings.",
	"admin organization":                      "Browse organization resources and settings.",
	"admin organization audit-logs":           "List organization actions and configuration changes.",
	"admin organization admin-api-keys":       "Create and manage organization admin API keys.",
	"admin organization usage":                "Inspect organization API usage and costs.",
	"admin organization invites":              "Invite users and manage organization invitations.",
	"admin organization users":                "Manage organization users and their roles.",
	"admin organization users roles":          "Assign and remove roles for organization users.",
	"admin organization groups":               "Manage organization groups, members, and roles.",
	"admin organization groups users":         "Manage users in an organization group.",
	"admin organization groups roles":         "Assign and remove roles for organization groups.",
	"admin organization roles":                "Create and manage organization roles.",
	"admin organization data-retention":       "Inspect and update organization data retention.",
	"admin organization external-storage":     "Manage organization external storage connections.",
	"admin organization spend-limit":          "Inspect and update the organization spend limit.",
	"admin organization spend-alerts":         "Manage organization spend alerts.",
	"admin organization certificates":         "Manage organization certificates.",
	"admin organization projects":             "Manage projects and their scoped resources.",
	"admin organization projects users":       "Manage project users and their roles.",
	"admin organization projects users roles": "Assign and remove roles for project users.",
	"admin organization projects service-accounts":          "Manage project service accounts and their API keys.",
	"admin organization projects service-accounts api-keys": "Create service account API keys.",
	"admin organization projects api-keys":                  "Inspect and delete project API keys.",
	"admin organization projects rate-limits":               "Inspect and update project rate limits.",
	"admin organization projects model-permissions":         "Manage project model permissions.",
	"admin organization projects hosted-tool-permissions":   "Manage project hosted-tool permissions.",
	"admin organization projects groups":                    "Manage project groups and their roles.",
	"admin organization projects groups roles":              "Assign and remove roles for project groups.",
	"admin organization projects roles":                     "Create and manage project roles.",
	"admin organization projects data-retention":            "Inspect and update project data retention.",
	"admin organization projects spend-limit":               "Inspect and update the project spend limit.",
	"admin organization projects spend-alerts":              "Manage project spend alerts.",
	"admin organization projects certificates":              "Manage project certificates.",
}

type commandDisplaySection struct {
	title string
	names string
}

// Display sections never replace Category: the subgroup adapter uses the
// generated API RESOURCE category to recognize resource nodes safely.
var commandDisplaySections = map[string][]commandDisplaySection{
	"images": {
		{"Actions", "generate edit preview models"},
		{"Settings", "inline"},
		{"Retired commands", "create-variation"},
	},
	"": {
		{"Shortcuts", "transcribe translate speak projects"},
		{"Generate content", "responses chat completions images audio videos"},
		{"Live and realtime", "live realtime"},
		{"Data and retrieval", "files uploads vector-stores embeddings conversations"},
		{"Tools and integrations", "containers skills webhooks"},
		{"Models and jobs", "models fine-tuning batches"},
		{"Safety and provenance", "moderations content-provenance-checks safety"},
		{"Administration", "admin"},
		{"Beta APIs", "beta"},
	},
	"admin organization": {
		{"Access", "users groups roles invites admin-api-keys"},
		{"Projects", "projects"},
		{"Usage and spending", "usage spend-limit spend-alerts"},
		{"Security and data", "audit-logs certificates data-retention external-storage"},
	},
	"admin organization projects": {
		{"Access", "users groups roles service-accounts api-keys"},
		{"Limits and permissions", "rate-limits model-permissions hosted-tool-permissions"},
		{"Spending", "spend-limit spend-alerts"},
		{"Security and data", "certificates data-retention"},
	},
}

func configureCommandPresentation(root *cli.Command) {
	var visit func(*cli.Command, string)
	visit = func(command *cli.Command, path string) {
		if command.Metadata == nil {
			command.Metadata = map[string]any{}
		}
		if path == "images" {
			command.Metadata["help-preserve-command-order"] = true
		}
		canonicalPath := path
		if resource, ok := command.Metadata[resourceCommandMetadata].(string); ok && command.Category == "API RESOURCE" {
			canonicalPath = strings.ReplaceAll(resource, ":", " ")
		}
		if command.Category == "API RESOURCE" && command.Usage == "" {
			command.Usage = commandGroupDescriptions[canonicalPath]
			if command.Usage == "" {
				parts := strings.Fields(path)
				name := command.Name
				if len(parts) > 0 {
					name = parts[len(parts)-1]
				}
				command.Usage = "Browse " + strings.ReplaceAll(name, "-", " ") + " commands."
				command.Metadata["help-fallback-description"] = true
			}
		}
		for _, child := range command.Commands {
			childPath := strings.TrimSpace(path + " " + strings.ReplaceAll(child.Name, ":", " "))
			visit(child, childPath)
		}
		if path == "admin" {
			canonicalPath = "admin organization"
		}
		sections, ok := commandDisplaySections[canonicalPath]
		if !ok {
			return
		}
		for _, child := range command.Commands {
			if child.Category == "API RESOURCE" {
				child.Metadata["help-command-section"] = "Other API resources"
				child.Metadata["help-command-rank"] = 10000
			}
		}
		rank := 0
		for _, section := range sections {
			for _, name := range strings.Fields(section.names) {
				rank++
				if child := command.Command(name); child != nil {
					child.Metadata["help-command-section"] = section.title
					child.Metadata["help-command-rank"] = rank
				}
			}
		}
	}
	visit(root, "")
}
