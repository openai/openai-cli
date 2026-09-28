package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImageDemoDelay(t *testing.T) {
	t.Setenv("DEMO_IMAGE_REQUEST_LOG", "")
	t.Setenv("DEMO_IMAGE_PREVIEW_FIXTURE", "")
	for _, value := range []string{"", "0s", "invalid", "-1s", "6s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("DEMO_IMAGE_DELAY", value)
			request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"A tiny orange robot"}`))
			request.Header.Set("Authorization", "Bearer synthetic-demo-key")
			response := httptest.NewRecorder()
			serveImageGeneration(response, request)
			if value == "" || value == "0s" {
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), syntheticImagePNG) {
					t.Fatalf("expected unchanged fixture response: %d %s", response.Code, response.Body)
				}
			} else if response.Code != http.StatusInternalServerError {
				t.Fatalf("invalid delay accepted: %d", response.Code)
			}
		})
	}
}

func TestImageDemoDelayHonorsCancellation(t *testing.T) {
	t.Setenv("DEMO_IMAGE_DELAY", "5s")
	t.Setenv("DEMO_IMAGE_REQUEST_LOG", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"A tiny orange robot"}`))
	request.Header.Set("Authorization", "Bearer synthetic-demo-key")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		serveImageGeneration(response, request)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("response completed before the configured delay")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
		if response.Body.Len() != 0 {
			t.Fatalf("canceled fixture returned image bytes: %s", response.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("fixture did not stop waiting after cancellation")
	}
}
