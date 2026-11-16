package commandsuggest

import (
	"slices"
	"strings"
)

// A verb-first request can name any installed action, including a new one.
// Shared paraphrases only identify the operation. Its resource must still match.
type resourceRequest struct {
	verb, operation string
	resource        []string
}

func parseResourceRequest(query string, verbs map[string]bool) resourceRequest {
	query = strings.TrimSuffix(query, "?")
	for _, prefix := range []string{"how do i ", "how can i ", "i want to ", "please "} {
		query = strings.TrimPrefix(query, prefix)
	}
	verb, subject, ok := strings.Cut(query, " ")
	if !ok {
		return resourceRequest{}
	}
	operation := verb
	switch verb {
	case "look":
		subject, ok = strings.CutPrefix(subject, "up ")
		if !ok {
			return resourceRequest{}
		}
		operation = "retrieve"
	case "enumerate":
		operation = "list"
	case "find":
		switch {
		case strings.HasPrefix(subject, "all "):
			operation = "list"
		case strings.HasSuffix(subject, " by id"):
			operation = "retrieve"
		}
	case "start":
		if strings.HasPrefix(subject, "a new ") || strings.HasPrefix(subject, "new ") {
			operation = "create"
		}
	case "show", "see", "display", "get", "fetch":
		subject = strings.TrimPrefix(subject, "me ")
		// An ID or a singular article identifies one item. A collection
		// qualifier or plural noun identifies a list.
		object, _, _ := strings.Cut(subject, " in ")
		object, _, _ = strings.Cut(object, " for ")
		collection := strings.HasPrefix(object, "all ") || (strings.HasSuffix(object, "s") &&
			!strings.HasSuffix(object, "ss") && !strings.HasSuffix(object, "us") &&
			!strings.HasPrefix(object, "a ") && !strings.HasPrefix(object, "an "))
		if collection && !strings.HasSuffix(subject, " by id") {
			operation = "list"
		} else {
			operation = "retrieve"
		}
	case "make", "add":
		operation = "create"
	case "modify", "change", "edit":
		operation = "update"
	case "remove":
		operation = "delete"
	}
	if !verbs[verb] && !verbs[operation] {
		return resourceRequest{}
	}
	// Do not reduce switches, operands, or shell expressions to resource names.
	if strings.HasPrefix(subject, "-") || strings.Contains(subject, " -") || strings.IndexFunc(subject, func(r rune) bool {
		return !searchWordRune(r) && r != '-' && r != ' '
	}) >= 0 {
		return resourceRequest{}
	}
	if operation == "retrieve" {
		subject = strings.TrimSuffix(subject, " by id")
	}
	// "List files in a vector store" names the parent after the child.
	object, parent, scoped := strings.Cut(subject, " in ")
	if !scoped {
		object, parent, scoped = strings.Cut(subject, " for ")
	}
	object = resourceNoun(object, operation)
	if object == "" {
		return resourceRequest{}
	}
	if scoped {
		parent = resourceNoun(parent, "")
		if parent == "" {
			return resourceRequest{}
		}
		object = parent + " " + object
	}
	return resourceRequest{verb, operation, strings.Fields(strings.ReplaceAll(object, "-", " "))}
}

func resourceNoun(noun, operation string) string {
	if operation == "list" {
		noun = strings.TrimPrefix(noun, "all ")
	}
	for _, prefix := range []string{"the ", "my ", "a ", "an "} {
		noun = strings.TrimPrefix(noun, prefix)
	}
	if operation == "create" {
		noun = strings.TrimPrefix(noun, "new ")
	}
	return noun
}

func (request resourceRequest) match(candidate *Candidate, entry *searchEntry, rank *[6]int) bool {
	cmd := candidate.definition
	if cmd == nil || len(request.resource) > entry.resourceWords {
		return false
	}
	literalVerb := request.verb == cmd.Name || slices.Contains(cmd.Aliases, request.verb)
	if !literalVerb && request.operation != cmd.Name {
		return false
	}
	// Only omit leading namespaces. Never drop a child to make its parent's
	// request match (e.g. deleting a store versus deleting a file in it).
	start := entry.resourceWords - len(request.resource)
	for i, word := range request.resource {
		target := entry.routeWords[start+i]
		if word != target && !pluralPair(word, target) {
			return false
		}
	}
	rank[0] = start
	if !literalVerb {
		rank[1] = 1
	}
	rank[5] = entry.length
	return true
}
