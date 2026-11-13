package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindows757PreservedRoutes(t *testing.T) {
	exe := os.Getenv("OPENAI_757_SHIPPING_EXE")
	if !filepath.IsAbs(exe) {
		t.Fatal("SHIPPING_EXE must identify an absolute executable path")
	}
	const caption = "Synthetic translated caption."
	const token = "synthetic-pr757-route-key"
	audio := []byte("RIFF\x28\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\xc0\x5d\x00\x00\x80\xbb\x00\x00\x02\x00\x10\x00data\x04\x00\x00\x00\x01\x00\x02\x00")

	for _, tc := range []struct {
		name           string
		route          []string
		jsonInput      bool
		redirectedLive bool
	}{
		{name: "FiniteAudioFlags", route: []string{"audio", "translate"}},
		{name: "FiniteAudioJSON", route: []string{"audio", "translate"}, jsonInput: true},
		{name: "FiniteTopLevelFlags", route: []string{"translate"}},
		{name: "FiniteGeneratedColonFlags", route: []string{"audio:translations", "create"}},
		{name: "FiniteGeneratedSpacedFlags", route: []string{"audio", "translations", "create"}},
		{name: "RedirectedLiveInput", route: []string{"audio", "translate-live"}, redirectedLive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			filename := "synthetic input.wav"
			path := filepath.Join(home, filename)
			if err := os.WriteFile(path, audio, 0600); err != nil {
				t.Fatal(err)
			}

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.redirectedLive || r.Method != http.MethodPost || r.URL.RequestURI() != "/v1/audio/translations" ||
					r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("UNEXPECTED_REQUEST")
					http.Error(w, "invalid route", http.StatusBadRequest)
					return
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error("INVALID_MULTIPART")
					http.Error(w, "invalid upload", http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				model := r.MultipartForm.Value["model"]
				files := r.MultipartForm.File["file"]
				if len(model) != 1 || model[0] != "whisper-1" || len(files) != 1 ||
					len(r.MultipartForm.Value) != 1 || len(r.MultipartForm.File) != 1 || files[0].Filename != filename {
					t.Error("UPLOAD_FIELDS_CHANGED")
					http.Error(w, "invalid fields", http.StatusBadRequest)
					return
				}
				file, err := files[0].Open()
				if err != nil {
					t.Error("UPLOAD_OPEN_FAILED")
					http.Error(w, "unreadable upload", http.StatusBadRequest)
					return
				}
				defer file.Close()
				got, err := io.ReadAll(file)
				if err != nil || !bytes.Equal(audio, got) {
					t.Error("UPLOAD_BYTES_CHANGED")
					http.Error(w, "invalid audio", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"text": caption})
			}))
			defer server.Close()

			args := []string{"--api-key", token, "--base-url", server.URL + "/v1/"}
			var input []byte
			if tc.jsonInput {
				args = append(args, "--format", "json")
				var err error
				input, err = json.Marshal(map[string]string{"file": path, "model": "whisper-1"})
				if err != nil {
					t.Fatal(err)
				}
			}
			args = append(args, tc.route...)
			if tc.redirectedLive {
				input = []byte("synthetic redirected input\n")
			} else if !tc.jsonInput {
				args = append(args, "--file", path, "--model", "whisper-1")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, exe, args...)
			child.Dir = home
			child.WaitDelay = time.Second
			child.Env = []string{
				"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home,
				"XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "TMPDIR=" + home, "TMP=" + home, "TEMP=" + home,
				"PATH=" + home, "PATHEXT=.EXE", "NO_COLOR=1", "OPENAI_UNTRUSTED_STDIN=false",
			}
			if root := os.Getenv("SystemRoot"); root != "" {
				child.Env = append(child.Env, "SystemRoot="+root, "WINDIR="+root)
			}
			child.Stdin = bytes.NewReader(input)
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			err := child.Run()
			if ctx.Err() != nil {
				t.Fatal("COMMAND_TIMEOUT")
			}
			if tc.redirectedLive {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || requests.Load() != 0 ||
					stdout.Len() != 0 || !strings.Contains(stderr.String(), "needs an interactive terminal") {
					t.Fatalf("REDIRECT_BEHAVIOR_CHANGED: exit=%v requests=%d", err, requests.Load())
				}
				return
			}
			if err != nil || requests.Load() != 1 || stderr.Len() != 0 {
				t.Fatalf("FINITE_COMMAND_FAILED: exit=%v requests=%d stderr_bytes=%d", err, requests.Load(), stderr.Len())
			}
			if tc.jsonInput {
				var result map[string]string
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result) != 1 || result["text"] != caption {
					t.Fatal("JSON_OUTPUT_CHANGED")
				}
			} else if stdout.String() != caption+"\n" {
				t.Fatal("TEXT_OUTPUT_CHANGED")
			}
		})
	}
}
