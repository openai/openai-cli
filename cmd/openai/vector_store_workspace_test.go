package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMainVectorStoreWorkspaceRouting(t *testing.T) {
	server, requests := vectorStoreSearchServer(t, http.StatusTeapot, `{}`)
	group := runVectorStoreCommand(t, server, nil, "vector-stores")
	assert.Equal(t, 0, group.code)
	assert.Contains(t, group.stdout, "manage")
	assert.Empty(t, group.stderr)

	unknown := runVectorStoreCommand(t, server, nil, "vector-stores", "synthetic-private-operand")
	assert.Equal(t, 3, unknown.code)
	assert.Empty(t, unknown.stdout)
	assert.Equal(t, "Unknown command. Run openai help vector-stores to see commands.\n", unknown.stderr)

	manage := runVectorStoreCommand(t, server, nil, "vector-stores", "manage")
	assert.Equal(t, 1, manage.code)
	assert.Empty(t, manage.stdout)
	assert.Equal(t, "The vector store workspace needs terminal input and output with cursor support. Use vector-stores list, create, files, or search in scripts.\n", manage.stderr)
	assert.Zero(t, requests.Load())
}
