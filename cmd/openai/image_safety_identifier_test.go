package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainImageSafetyIdentifier(t *testing.T) {
	const identifier = "synthetic-image-safety-user"
	const legacyUser = "synthetic-legacy-user"
	for _, operation := range []string{"generate", "edit"} {
		for _, streaming := range []bool{false, true} {
			for _, field := range []struct {
				name, flag, response string
				present              bool
			}{
				{"value", identifier, `"` + identifier + `"`, true},
				{"null", "null", "null", true},
				{"omitted", "", "", false},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", operation, streaming, field.name), func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						require.Equal(t, http.MethodPost, r.Method)
						if operation == "generate" {
							require.Equal(t, "/images/generations", r.URL.Path)
							var body map[string]any
							require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
							require.Equal(t, legacyUser, body["user"])
							value, present := body["safety_identifier"]
							require.Equal(t, field.present, present)
							if field.name == "value" {
								require.Equal(t, identifier, value)
							} else {
								require.Nil(t, value)
							}
						} else {
							body := readImageUpload(t, r)
							require.Equal(t, "/images/edits", body.path)
							require.Equal(t, []string{legacyUser}, body.values["user"])
							value, present := body.values["safety_identifier"]
							require.Equal(t, field.present, present)
							if field.name == "value" {
								require.Equal(t, []string{identifier}, value)
							} else if field.name == "null" {
								// Multipart encodes a nullable scalar as an empty field.
								require.Equal(t, []string{""}, value)
							}
						}
						payload := `{"created":17,"data":[]`
						if streaming {
							event := "image_generation.completed"
							if operation == "edit" {
								event = "image_edit.completed"
							}
							payload = fmt.Sprintf(`{"type":%q,"b64_json":"synthetic-image"`, event)
						}
						if field.present {
							payload += `,"safety_identifier":` + field.response
						}
						payload += "}"
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "data: %s\n\n", payload)
						} else {
							w.Header().Set("Content-Type", "application/json")
							io.WriteString(w, payload)
						}
					}))
					defer server.Close()
					args := []string{"--format", "json", "images", operation, "--prompt", "synthetic test", "--user", legacyUser}
					if operation == "edit" {
						source, _ := imageUploadSource(t)
						args = append(args, "--image", source)
					}
					if field.present {
						args = append(args, "--safety-identifier", field.flag)
					}
					if streaming {
						args = append(args, "--stream", "true")
					}
					got := runImageGeneration(t, server, t.TempDir(), "", args...)
					require.Zero(t, got.code, got.stderr)
					require.Empty(t, got.stderr)
					require.Equal(t, int32(1), requests.Load())
					decoder := json.NewDecoder(strings.NewReader(got.stdout))
					var result map[string]any
					require.NoError(t, decoder.Decode(&result))
					value, present := result["safety_identifier"]
					require.Equal(t, field.present, present)
					if field.name == "value" {
						require.Equal(t, identifier, value)
					} else {
						require.Nil(t, value)
					}
					var extra any
					require.ErrorIs(t, decoder.Decode(&extra), io.EOF)
				})
			}
		}
	}
}
