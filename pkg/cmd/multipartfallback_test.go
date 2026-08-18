package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestFilesCreateCLIStreamsWhenSnapshotDirectoryUnavailable(t *testing.T) {
	for _, payload := range [][]byte{[]byte("streamed payload"), {}} {
		name := "non_empty"
		if len(payload) == 0 {
			name = "zero_byte"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upload.bin")
			require.NoError(t, os.WriteFile(path, payload, 0o600))
			setUnavailableTempDir(t)

			received := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contents, err := readMultipartUpload(r)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				received <- contents
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
			}))
			t.Cleanup(server.Close)

			require.NoError(t, runFilesCreateCLI(t.Context(), server.URL+"/", path))
			require.Equal(t, payload, <-received)
		})
	}
}

func TestFilesCreateCLIDisablesReplayWhenSnapshotDirectoryUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusTooManyRequests} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upload.bin")
			require.NoError(t, os.WriteFile(path, []byte("first attempt only"), 0o600))
			setUnavailableTempDir(t)

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				contents, err := readMultipartUpload(r)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				require.Equal(t, []byte("first attempt only"), contents)
				if status == http.StatusTemporaryRedirect {
					http.Redirect(w, r, "/upload", status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"retry unavailable","type":"rate_limit_error"}}`)
			}))
			t.Cleanup(server.Close)

			err := runFilesCreateCLI(t.Context(), server.URL+"/", path)
			require.Error(t, err)
			require.Equal(t, int32(1), requests.Load(), "snapshot failure must disable network replay")
		})
	}
}

func TestVideosCreateCLIPropagatesExtensionlessDirectoryError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.Mkdir("artifactdir", 0o700))

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)

	err := runVideosCreateCLI(t.Context(), server.URL+"/", "@artifactdir")
	require.ErrorContains(t, err, "is a directory")
	require.Zero(t, requests.Load(), "an existing invalid upload must not fall back to a scalar")
}

func TestVideosCreateCLIUploadsExtensionlessFileWhenSnapshotDirectoryUnavailable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("artifact", []byte("extensionless payload"), 0o600))
	setUnavailableTempDir(t)

	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("input_reference")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		contents, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- contents
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)

	require.NoError(t, runVideosCreateCLI(t.Context(), server.URL+"/", "@artifact"))
	require.Equal(t, []byte("extensionless payload"), <-received)
}

func setUnavailableTempDir(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
}

func runVideosCreateCLI(ctx context.Context, baseURL, inputReference string) error {
	create := videosCreate
	command := &cli.Command{
		Name: "openai",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "format", Value: "json"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
			&cli.StringFlag{Name: "api-key"},
		},
		Commands: []*cli.Command{{
			Name:     "videos",
			Commands: []*cli.Command{&create},
		}},
	}
	return command.Run(ctx, []string{
		"openai",
		"--api-key", "test-key",
		"--base-url", baseURL,
		"videos", "create",
		"--prompt", "hello",
		"--input-reference", inputReference,
	})
}

func runImagesEditCLI(ctx context.Context, baseURL string, paths []string) error {
	edit := imagesEdit
	command := &cli.Command{
		Name: "openai",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "format", Value: "json"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
			&cli.StringFlag{Name: "api-key"},
		},
		Commands: []*cli.Command{{
			Name:     "images",
			Commands: []*cli.Command{&edit},
		}},
	}
	args := []string{
		"openai",
		"--api-key", "test-key",
		"--base-url", baseURL,
		"images", "edit",
		"--prompt", "hello",
	}
	for _, path := range paths {
		args = append(args, "--image", path)
	}
	return command.Run(ctx, args)
}
