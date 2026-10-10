// schema-helper-demo-api serves one synthetic Responses schema artifact.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const model = "gpt-4.1-mini-2025-04-14"
const description = "An invoice with line items"

const invoiceSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "invoice_id": {"type": "string"},
    "line_items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "description": {"type": "string"},
          "quantity": {"type": "integer"},
          "unit_price": {"type": "number"}
        },
        "required": ["description", "quantity", "unit_price"],
        "additionalProperties": false
      }
    }
  },
  "required": ["invoice_id", "line_items"],
  "additionalProperties": false
}
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (result error) {
	if len(os.Args) != 4 {
		return errors.New("usage: schema-helper-demo-api ADDRESS_FILE REQUEST_LOG EXPECTED_ARTIFACT")
	}
	requests, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, requests.Close()) }()
	if err := writeExclusive(os.Args[3], invoiceSchema); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}()
	var logMu sync.Mutex
	var handlerErr error
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.URL.RawQuery != "" ||
			r.Header.Get("Authorization") != "Bearer synthetic-demo-key" {
			http.Error(w, "only the synthetic schema request is accepted", http.StatusNotFound)
			return
		}
		var body struct {
			Model           string `json:"model"`
			Input           string `json:"input"`
			Instructions    string `json:"instructions"`
			Store           *bool  `json:"store"`
			MaxOutputTokens int    `json:"max_output_tokens"`
			Text            struct {
				Format struct {
					Type string `json:"type"`
				} `json:"format"`
			} `json:"text"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(w, "invalid synthetic request body", http.StatusBadRequest)
			return
		}
		if _, err := decoder.Token(); err != io.EOF || body.Model != model || body.Input != description ||
			body.Store == nil || *body.Store || body.MaxOutputTokens != 8192 ||
			body.Text.Format.Type != "json_object" || !strings.Contains(body.Instructions, "JSON Schema Draft 2020-12") {
			http.Error(w, "request differs from the synthetic fixture contract", http.StatusBadRequest)
			return
		}
		// Log only validated synthetic fields. Never record headers or instructions.
		logMu.Lock()
		err := json.NewEncoder(requests).Encode(map[string]any{
			"method": "POST", "path": "/v1/responses", "model": model,
			"synthetic_description_matched": true, "store": false,
			"max_output_tokens": 8192, "format": "json_object",
		})
		handlerErr = errors.Join(handlerErr, err)
		logMu.Unlock()
		if err != nil {
			http.Error(w, "could not record synthetic request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_synthetic_schema", "object": "response", "status": "completed", "model": model,
			"output": []any{map[string]any{
				"type": "message", "id": "msg_synthetic_schema", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": invoiceSchema, "annotations": []any{}}},
			}},
		})
		logMu.Lock()
		handlerErr = errors.Join(handlerErr, err)
		logMu.Unlock()
	})
	if err := writeExclusive(os.Args[1], "http://"+listener.Addr().String()+"/v1"); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		shutdownDone <- server.Shutdown(shutdown)
	}()
	err = server.Serve(listener)
	cancel()
	shutdownErr := <-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	logMu.Lock()
	defer logMu.Unlock()
	return errors.Join(err, shutdownErr, handlerErr)
}

func writeExclusive(path, value string) (result error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	n, err := io.WriteString(file, value)
	if err == nil && n != len(value) {
		err = io.ErrShortWrite
	}
	return err
}
