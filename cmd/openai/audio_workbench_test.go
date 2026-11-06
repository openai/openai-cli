package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/stretchr/testify/require"
)

func audioWorkbenchWAV() []byte {
	const frames, rate = 800, 8000
	data := make([]byte, 44+frames*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], rate)
	binary.LittleEndian.PutUint32(data[28:32], rate*2)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:44], frames*2)
	binary.LittleEndian.PutUint16(data[44:46], 32768)
	binary.LittleEndian.PutUint16(data[len(data)-2:], 16384)
	return data
}

func audioWorkbenchHistory(t *testing.T) ([]string, audiohistory.Store, audiohistory.Entry, audiohistory.Entry, []byte) {
	t.Helper()
	home, cache, programs := t.TempDir(), t.TempDir(), t.TempDir()
	cacheBase := cache
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		cacheBase = filepath.Join(home, "Library", "Caches")
	}
	if runtime.GOOS == "windows" {
		cacheBase = home
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CACHE_HOME=" + cache,
		"XDG_CONFIG_HOME=" + home, "LOCALAPPDATA=" + home, "LocalAppData=" + home,
		"APPDATA=" + home, "PATH=" + programs, "OPENAI_PICKER_SHELL=off", "FORCE_COLOR=0",
		"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost"}
	store := audiohistory.Store{Directory: filepath.Join(cacheBase, "openai", "audio")}
	data := audioWorkbenchWAV()
	settings := audiohistory.Settings{Input: "Private synthetic prompt", Instructions: "Private synthetic instructions",
		Model: "synthetic-model", Voice: "coral", Format: "wav", Speed: 1}
	entry, err := store.Capture(t.Context(), io.NopCloser(bytes.NewReader(data)), settings)
	require.NoError(t, err)
	settings.Input, settings.Preview = "Synthetic voice audition", true
	preview, err := store.Capture(t.Context(), io.NopCloser(bytes.NewReader(data)), settings)
	require.NoError(t, err)
	return env, store, entry, preview, data
}

type audioWorkbenchHelperConfig struct {
	Hold bool
}

type audioWorkbenchHelperReceipt struct {
	PID                  int
	Args                 []string
	Input                []byte
	ForbiddenEnvironment []string
}

const audioWorkbenchHelperConfigName = "audio-workbench-helper.json"
const audioWorkbenchHelperReceiptName = "audio-workbench-helper-result.json"

// Copy the current test executable so public playback uses normal PATH lookup.
// Sidecar configuration does not weaken the production helper environment filter.
func prepareAudioWorkbenchHelper(t *testing.T, hold bool) (programs string, receiptPath string) {
	t.Helper()
	programs = t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	source, err := os.Open(executable)
	require.NoError(t, err)
	name := "ffplay"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	destination, err := os.OpenFile(filepath.Join(programs, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(errors.Join(err, source.Close()))
	}
	_, copyErr := io.Copy(destination, source)
	require.NoError(t, errors.Join(copyErr, destination.Close(), source.Close()))
	configuration, err := json.Marshal(audioWorkbenchHelperConfig{Hold: hold})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(programs, audioWorkbenchHelperConfigName), configuration, 0600))
	return programs, filepath.Join(programs, audioWorkbenchHelperReceiptName)
}

// TestMain dispatches copied ffplay executables before testing parses their flags.
func runAudioWorkbenchHelper() (bool, error) {
	name := filepath.Base(os.Args[0])
	if name != "ffplay" && name != "ffplay.exe" {
		return false, nil
	}
	deadline := time.AfterFunc(30*time.Second, func() { os.Exit(124) })
	defer deadline.Stop()
	executable, err := os.Executable()
	if err != nil {
		return true, err
	}
	directory := filepath.Dir(executable)
	configuration, err := os.ReadFile(filepath.Join(directory, audioWorkbenchHelperConfigName))
	if err != nil {
		return true, err
	}
	var config audioWorkbenchHelperConfig
	if err := json.Unmarshal(configuration, &config); err != nil {
		return true, err
	}
	if config.Hold {
		// Notify suppresses default Windows console-event termination; Ignore does not.
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		defer signal.Stop(interrupts)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return true, err
	}
	receipt := audioWorkbenchHelperReceipt{PID: os.Getpid(), Args: os.Args[1:], Input: input}
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "OPENAI_") || key == "FFREPORT" {
			receipt.ForbiddenEnvironment = append(receipt.ForbiddenEnvironment, key)
		}
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return true, err
	}
	path := filepath.Join(directory, audioWorkbenchHelperReceiptName)
	temporary := path + ".tmp"
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return true, err
	}
	if err := os.Rename(temporary, path); err != nil {
		return true, err
	}
	if config.Hold {
		select {} // The deadline bounds cleanup after an unexpected parent failure.
	}
	return true, nil
}

func requireNoAudioSnapshots(t *testing.T, store audiohistory.Store) {
	t.Helper()
	entries, err := os.ReadDir(store.Directory)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".snapshot-"), "operation retained a private snapshot")
	}
}

func TestMainAudioWorkbenchLocalHistoryAndInspectFormats(t *testing.T) {
	env, _, entry, preview, _ := audioWorkbenchHistory(t)
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected synthetic request", http.StatusUnauthorized)
	}))
	defer server.Close()
	for _, baseURL := range []string{server.URL, "://synthetic-private-endpoint"} {
		localEnv := append(slices.Clone(env), "OPENAI_BASE_URL="+baseURL,
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert", "OPENAI_CUSTOM_HEADERS=synthetic-private-invalid-headers")
		for _, format := range []string{"json", "jsonl", "raw"} {
			for _, args := range [][]string{
				{"openai", "--format", format, "audio", "history"},
				{"openai", "audio", "--format", format, "history"},
				{"openai", "audio", "history", "--format", format},
			} {
				got := runMainDispatchWithEnv(t, "bash", localEnv, args...)
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				var entries []audiohistory.Entry
				require.NoError(t, json.Unmarshal([]byte(got.stdout), &entries))
				require.Equal(t, []audiohistory.Entry{entry}, entries)
				require.NotContains(t, got.stdout, preview.ID)
			}
			got := runMainDispatchWithEnv(t, "bash", localEnv, "openai", "audio", "--format", format, "inspect", entry.ID)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			var report struct {
				Entry    audiohistory.Entry  `json:"entry"`
				Analysis audiomedia.Analysis `json:"analysis"`
			}
			require.NoError(t, json.Unmarshal([]byte(got.stdout), &report), "waveform prose corrupted machine output")
			require.Equal(t, entry, report.Entry)
			require.Equal(t, .1, report.Analysis.Duration)
			require.Contains(t, report.Analysis.Peaks, float64(1))
		}
		got := runMainDispatchWithEnv(t, "bash", localEnv, "openai", "--transform", "analysis.duration", "--raw-output", "audio", "inspect", entry.ID)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Equal(t, "0.1\n", got.stdout)
		got = runMainDispatchWithEnv(t, "bash", localEnv, "openai", "--transform", "0.id", "--raw-output", "audio", "history")
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Equal(t, entry.ID+"\n", got.stdout)
	}
	require.Zero(t, requests.Load(), "local audio commands reached the API")
}

func TestMainAudioWorkbenchExportPreservesBytesAndReceiptPolicy(t *testing.T) {
	env, _, entry, _, data := audioWorkbenchHistory(t)
	env = append(env, "OPENAI_BASE_URL=://synthetic-private-endpoint", "OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert")
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw"} {
		got := runMainDispatchWithEnv(t, "bash", env, "openai", "--format", format, "audio", "export", entry.ID, "--output", "-")
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Equal(t, data, []byte(got.stdout), "export decorated original audio")
	}
	for _, tc := range []struct {
		name  string
		flags []string
		quiet bool
	}{
		{"readable receipt", nil, false},
		{"quiet receipt", []string{"--quiet"}, true},
		{"structured receipt", []string{"--format", "json"}, true},
		{"structured error receipt", []string{"--format-error", "json"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "saved audio.wav")
			args := append([]string{"openai"}, tc.flags...)
			args = append(args, "audio", "export", entry.ID, "--output", path)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stdout)
			if tc.quiet {
				require.Empty(t, got.stderr)
			} else {
				require.Equal(t, "Wrote output to: "+path+"\n", got.stderr)
			}
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, saved)
		})
	}
}

func TestMainAudioWorkbenchClearPreservesExportedFiles(t *testing.T) {
	env, store, entry, _, data := audioWorkbenchHistory(t)
	path := filepath.Join(t.TempDir(), "exported.wav")
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "--quiet", "audio", "export", entry.ID, "--output", path)
	require.Zero(t, got.code, "%s", got.stderr)
	got = runMainDispatchWithEnv(t, "bash", env, "openai", "audio", "--format", "json", "history", "--clear")
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.JSONEq(t, `{"cleared":true}`, got.stdout)
	entries, err := store.List(t.Context())
	require.NoError(t, err)
	require.Empty(t, entries)
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, saved)
	got = runMainDispatchWithEnv(t, "bash", env, "openai", "--format", "json", "audio", "history")
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.JSONEq(t, `[]`, got.stdout)
}

func TestMainAudioWorkbenchLocalFailuresKeepPrivatePathsOut(t *testing.T) {
	env, store, entry, _, _ := audioWorkbenchHistory(t)
	env = append(env, "OPENAI_BASE_URL=://synthetic-private-endpoint", "OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert")
	for _, args := range [][]string{
		{"audio", "inspect", strings.Repeat("a", 32)},
		{"audio", "export", "../synthetic-private", "--output", "-"},
		{"audio", "play", entry.ID}, // Empty PATH provides a deterministic missing-player failure.
	} {
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--format-error", "json"}, args...)...)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		var diagnostic map[string]any
		require.NoError(t, json.Unmarshal([]byte(got.stderr), &diagnostic))
		for _, private := range []string{store.Directory, "synthetic-private", "Private synthetic prompt", "Private synthetic instructions"} {
			require.NotContains(t, got.stderr, private)
		}
	}
}

func TestMainAudioWorkbenchLocalNamesDoNotBypassRemoteSetup(t *testing.T) {
	env, _, _, _, _ := audioWorkbenchHistory(t)
	env = append(env, "OPENAI_BASE_URL=://synthetic-private-endpoint")
	for _, value := range []string{"history", "inspect", "play", "export"} {
		got := runMainDispatchWithEnv(t, "bash", env, "openai", "--organization", value,
			"audio", "speak", "--model", value, "--voice", "alloy", "--input", value)
		require.Equal(t, 1, got.code)
		require.Empty(t, got.stdout)
		require.Contains(t, got.stderr, "OPENAI_BASE_URL")
		require.NotContains(t, got.stderr, "synthetic-private-")
	}
}

func TestMainAudioWorkbenchPlayUsesSyntheticLocalHelper(t *testing.T) {
	env, store, entry, _, data := audioWorkbenchHistory(t)
	programs, receiptPath := prepareAudioWorkbenchHelper(t, false)
	env = append(env, "PATH="+programs, "OPENAI_BASE_URL=://synthetic-private-endpoint",
		"OPENAI_API_KEY=sk-fake-audio-playback", "FFREPORT=synthetic-private-report",
		"OPENAI_ADMIN_KEY=sk-fake-audio-admin", "OPENAI_WEBHOOK_SECRET=whsec-fake-audio",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert")
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "audio", "--format", "json", "play", entry.ID)
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stdout)
	require.Empty(t, got.stderr)
	recorded, err := os.ReadFile(receiptPath)
	require.NoError(t, err)
	var receipt audioWorkbenchHelperReceipt
	require.NoError(t, json.Unmarshal(recorded, &receipt))
	require.Positive(t, receipt.PID)
	require.Empty(t, receipt.ForbiddenEnvironment, "playback helper inherited private environment variables")
	require.Equal(t, data, receipt.Input, "playback helper did not receive the verified original samples")
	input := slices.Index(receipt.Args, "-i")
	require.GreaterOrEqual(t, input, 0)
	require.Less(t, input+1, len(receipt.Args))
	require.Equal(t, "pipe:0", receipt.Args[input+1], "playback helper must consume verified bytes through stdin")
	requireNoAudioSnapshots(t, store)
	retained, err := os.ReadFile(filepath.Join(store.Directory, entry.ID, "audio"))
	require.NoError(t, err)
	require.Equal(t, data, retained, "playback changed retained original bytes")
}

func TestMainAudioWorkbenchLeavesDirectSpeechBinaryAndSSEPaths(t *testing.T) {
	body := string(audioWorkbenchWAV())
	server := readableAudioServer(t, "audio:speech", "audio/wav", body)
	for _, command := range [][]string{{"audio", "speak"}, {"speak"}, {"audio:speech", "create"}} {
		args := append(slices.Clone(command), "--model", "fake-audio-model", "--voice", "alloy", "--input", "Synthetic speech", "--response-format", "wav")
		for _, format := range []string{"auto", "text", "json", "raw"} {
			got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Equal(t, body, got.stdout)
		}
	}
	const first = `{"type":"speech.audio.delta","audio":"c3ludGhldGlj"}`
	const last = `{"type":"speech.audio.done","usage":{"output_tokens":2}}`
	const wire = "data: " + first + "\r\n\r\ndata: " + last + "\r\n\r\ndata: [DONE]\r\n\r\n"
	stream := readableAudioServer(t, "audio:speech", "text/event-stream", wire)
	for _, command := range [][]string{{"audio", "speak"}, {"speak"}} {
		args := append(slices.Clone(command), "--model", "fake-audio-model", "--voice", "alloy", "--input", "Synthetic speech", "--stream-format", "sse")
		got := runReadableCommand(t, stream, append([]string{"--format", "raw"}, args...)...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Equal(t, wire, got.stdout)
		got = runReadableCommand(t, stream, append([]string{"--format", "jsonl"}, args...)...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		assertReadableAudioJSON(t, got.stdout, first, last)
	}
}

func TestMainAudioWorkbenchLicensesAreLocalAndPreserveFormats(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	var notice string
	for _, endpoint := range []string{server.URL, "://synthetic-private-endpoint"} {
		env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL="+endpoint,
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert", "OPENAI_CUSTOM_HEADERS=synthetic-private-headers")
		for _, argv := range [][]string{
			{"openai", "audio", "licenses", "--help"},
			{"openai", "help", "audio", "licenses"},
		} {
			got := runMainDispatchWithEnv(t, "bash", env, argv...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "audio licenses")
		}
		for _, format := range []string{"json", "jsonl", "raw"} {
			for _, argv := range [][]string{
				{"openai", "--format", format, "audio", "licenses"},
				{"openai", "audio", "--format", format, "licenses"},
				{"openai", "audio", "licenses", "--format", format},
			} {
				got := runMainDispatchWithEnv(t, "bash", env, argv...)
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				var value map[string]string
				require.NoError(t, json.Unmarshal([]byte(got.stdout), &value))
				if notice == "" {
					notice = value["licenses"]
					require.Contains(t, notice, "github.com/atotto/clipboard v0.1.4")
					require.Contains(t, notice, "Redistribution and use")
					require.Contains(t, notice, "THIS SOFTWARE IS PROVIDED")
				}
				require.Equal(t, map[string]string{"licenses": notice}, value)
				if format == "raw" {
					encoded, err := json.Marshal(value)
					require.NoError(t, err)
					require.Equal(t, string(encoded)+"\n", got.stdout)
				}
			}
		}
		got := runMainDispatchWithEnv(t, "bash", env, "openai", "--transform", "licenses", "--raw-output", "audio", "licenses")
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Equal(t, notice+"\n", got.stdout)
		got = runMainDispatchWithEnv(t, "bash", env, "openai", "audio", "licenses")
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Contains(t, got.stdout, "Redistribution and use")
	}
	require.Zero(t, requests.Load(), "license notices reached the API")
}
