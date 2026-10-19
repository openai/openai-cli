package audio

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func requestIDError(t *testing.T, status int, id string) *openai.Error {
	t.Helper()
	header := make(http.Header)
	header.Set("X-Request-ID", id)
	apierr := &openai.Error{StatusCode: status, Response: &http.Response{Header: header}}
	require.NoError(t, json.Unmarshal([]byte(`{"message":"synthetic body detail","type":"server_error","future_field":{"sequence":9007199254740993}}`), apierr))
	return apierr
}
