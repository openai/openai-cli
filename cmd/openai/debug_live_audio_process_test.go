//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/stretchr/testify/require"
)

func TestMainDebugLiveAudioPreservesWebSocket(t *testing.T) {
	for _, mode := range []struct {
		name  string
		flags []string
	}{
		{"ordinary", nil},
		{"debug", []string{"--debug"}},
		{"capture", []string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing", "--debug-max-bytes", "64", "--debug-max-events", "1"}},
	} {
		for _, drain := range []bool{true, false} {
			ending := "cancel"
			if drain {
				ending = "drain"
			}
			t.Run(mode.name+"/"+ending, func(t *testing.T) {
				server, observed, upstreamDone := debugLiveAudioUpstream(t, drain)
				home := t.TempDir()
				// Private launchers keep the real command away from the desktop browser.
				for _, name := range []string{"open", "xdg-open"} {
					require.NoError(t, os.WriteFile(filepath.Join(home, name), []byte("#!/bin/sh\nexit 0\n"), 0700))
				}
				env := []string{
					"PATH=" + home, "HOME=" + home, "TMPDIR=" + home,
					"XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home,
					"OPENAI_API_KEY=sk-fake-debug-capture", "OPENAI_BASE_URL=" + server.URL + "/v1/",
					"FORCE_COLOR=0",
				}
				stdout := &debugTimingCapture{changed: make(chan struct{}, 1)}
				args := append(append([]string(nil), mode.flags...), "audio", "translate", "--live")
				child, stderr, done, ctx := startAudioLocalProcess(t, env, stdout, args...)
				stdout.waitFor(t, ctx, "Ctrl+C to close.\n")
				lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
				require.Len(t, lines, 4)
				launch := lines[2]
				client, origin, token := debugLiveAudioCompanion(t, launch)
				request := func(method, path string, body io.Reader, status int) *http.Response {
					t.Helper()
					req, err := http.NewRequestWithContext(ctx, method, origin+path, body)
					require.NoError(t, err)
					req.Header.Set("Origin", origin)
					req.Header.Set("X-OpenAI-Audio", token)
					response, err := client.Do(req)
					require.NoError(t, err)
					t.Cleanup(func() { response.Body.Close() })
					if response.StatusCode != status {
						body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
						t.Fatalf("live companion %s returned %d, want %d: %s", path, response.StatusCode, status, body)
					}
					return response
				}
				started := request(http.MethodPost, "/start", strings.NewReader(`{"language":"fr","noise_reduction":true}`), http.StatusOK)
				var session struct{ ID string }
				require.NoError(t, json.NewDecoder(started.Body).Decode(&session))
				started.Body.Close()
				require.NotEmpty(t, session.ID)
				select {
				case config := <-observed:
					require.JSONEq(t, `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"gpt-realtime-whisper"},"noise_reduction":{"type":"near_field"}},"output":{"language":"fr"}}}}`, string(config))
				case <-ctx.Done():
					t.Fatal("upstream did not receive session configuration")
				}
				events := request(http.MethodGet, "/events?id="+session.ID, nil, http.StatusOK)
				pcm := bytes.Repeat([]byte{0x12, 0x34}, 40000)
				request(http.MethodPost, "/input?id="+session.ID+"&sequence=0", bytes.NewReader(pcm), http.StatusNoContent).Body.Close()
				select {
				case data := <-observed:
					var audio struct{ Type, Audio string }
					require.NoError(t, json.Unmarshal(data, &audio))
					require.Equal(t, "session.input_audio_buffer.append", audio.Type)
					decoded, err := base64.StdEncoding.DecodeString(audio.Audio)
					require.NoError(t, err)
					require.Equal(t, pcm, decoded)
				case <-ctx.Done():
					t.Fatal("upstream did not receive audio")
				}
				decoder := json.NewDecoder(events.Body)
				for _, expected := range []audiolive.Event{
					{Type: "session.input_transcript.delta", Delta: "synthetic-private-input"},
					{Type: "session.output_audio.delta", Delta: "AQIDBA==", SampleRate: 48000, Channels: 2, Format: "pcm16"},
				} {
					var event audiolive.Event
					require.NoError(t, decoder.Decode(&event))
					require.Equal(t, expected, event)
				}
				if drain {
					request(http.MethodPost, "/stop?id="+session.ID, nil, http.StatusNoContent).Body.Close()
					for _, expected := range []audiolive.Event{
						{Type: "session.output_transcript.delta", Delta: "synthetic-private-output"},
						{Type: "session.closed"},
					} {
						var event audiolive.Event
						require.NoError(t, decoder.Decode(&event))
						require.Equal(t, expected, event)
					}
					select {
					case err := <-upstreamDone:
						require.NoError(t, err)
					case <-ctx.Done():
						t.Fatal("drained session retained its upstream connection")
					}
				}
				require.NoError(t, child.Process.Signal(os.Interrupt))
				select {
				case err := <-done:
					var exit *exec.ExitError
					require.ErrorAs(t, err, &exit)
					require.Equal(t, 130, exit.ExitCode(), "stderr: %s", stderr.String())
				case <-ctx.Done():
					t.Fatal("live translation did not exit after SIGINT")
				}
				if !drain {
					select {
					case err := <-upstreamDone:
						require.NoError(t, err)
					case <-ctx.Done():
						t.Fatal("cancellation retained its upstream connection")
					}
				}
				require.NoError(t, ctx.Err())
				_, err := os.Stat(launch)
				require.ErrorIs(t, err, os.ErrNotExist, "private launch file survived CLI exit")
				require.Equal(t, "Live translation opened in your browser.\nIf the page does not load, open this private launch page in Chrome or Edge:\n"+launch+"\nCtrl+C to close.\n", stdout.String())
				log := stderr.String()
				if mode.name == "ordinary" {
					require.Empty(t, log)
				} else {
					assertDebugTimingStage(t, log, 1, "response headers received", "101 Switching Protocols")
				}
				assertDebugCapturePrivate(t, log, "synthetic-private-input", "synthetic-private-output", "AQIDBA==", base64.StdEncoding.EncodeToString(pcm[:12]))
			})
		}
	}
}

func debugLiveAudioCompanion(t *testing.T, launch string) (*http.Client, string, string) {
	t.Helper()
	data, err := os.ReadFile(launch)
	require.NoError(t, err)
	_, navigation, ok := strings.Cut(string(data), "location.replace(")
	require.True(t, ok)
	encoded, _, ok := strings.Cut(navigation, ");")
	require.True(t, ok)
	var address string
	require.NoError(t, json.Unmarshal([]byte(encoded), &address))
	u, err := url.Parse(address)
	require.NoError(t, err)
	require.Equal(t, "http", u.Scheme)
	require.True(t, strings.HasSuffix(u.Hostname(), ".localhost"))
	require.NotEmpty(t, u.Port())
	require.NotEmpty(t, u.Fragment)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", u.Port()))
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}, u.Scheme + "://" + u.Host, u.Fragment
}

func debugLiveAudioUpstream(t *testing.T, drain bool) (*httptest.Server, <-chan []byte, <-chan error) {
	t.Helper()
	observed := make(chan []byte, 2)
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done <- func() error {
			if r.URL.Path != "/v1/realtime/translations" || r.URL.Query().Get("model") != "gpt-realtime-translate" || r.Header.Get("Authorization") != "Bearer sk-fake-debug-capture" {
				http.Error(w, "unexpected synthetic handshake", http.StatusBadRequest)
				return fmt.Errorf("unexpected synthetic handshake")
			}
			conn, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return err
			}
			defer conn.Close()
			t.Cleanup(func() { conn.Close() })
			if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return err
			}
			accept := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			if _, err := fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:])); err != nil {
				return err
			}
			if err := buffer.Flush(); err != nil {
				return err
			}
			send := func(data string) error {
				if len(data) >= 126 {
					return fmt.Errorf("synthetic server frame exceeds short-frame fixture")
				}
				_, err := conn.Write(append([]byte{0x81, byte(len(data))}, data...))
				return err
			}
			if err := send(`{"type":"session.created"}`); err != nil {
				return err
			}
			config, err := debugLiveAudioReadFrame(buffer.Reader)
			if err != nil {
				return err
			}
			observed <- config
			if err := send(`{"type":"session.updated"}`); err != nil {
				return err
			}
			audio, err := debugLiveAudioReadFrame(buffer.Reader)
			if err != nil {
				return err
			}
			observed <- audio
			for _, event := range []string{
				`{"type":"session.input_transcript.delta","delta":"synthetic-private-input"}`,
				`{"type":"session.output_audio.delta","delta":"AQIDBA==","sample_rate":48000,"channels":2,"format":"pcm16"}`,
			} {
				if err := send(event); err != nil {
					return err
				}
			}
			if drain {
				closed, err := debugLiveAudioReadFrame(buffer.Reader)
				if err != nil {
					return err
				}
				var event struct{ Type string }
				if err := json.Unmarshal(closed, &event); err != nil || event.Type != "session.close" {
					return fmt.Errorf("client did not finish translation input")
				}
				for _, event := range []string{`{"type":"session.output_transcript.delta","delta":"synthetic-private-output"}`, `{"type":"session.closed"}`} {
					if err := send(event); err != nil {
						return err
					}
				}
			}
			if _, err := debugLiveAudioReadFrame(buffer.Reader); err != io.EOF {
				return fmt.Errorf("client retained or reused the finished WebSocket: %v", err)
			}
			return nil
		}()
	}))
	t.Cleanup(server.Close)
	return server, observed, done
}

// This fixture accepts only masked text and close frames from the synthetic SDK client.
func debugLiveAudioReadFrame(reader *bufio.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	if header[0]&0x0f == 8 {
		return nil, io.EOF
	}
	if header[0] != 0x81 || header[1]&0x80 == 0 {
		return nil, fmt.Errorf("unexpected synthetic WebSocket frame")
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	} else if length == 127 {
		var extended [8]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	if length > 1<<20 {
		return nil, fmt.Errorf("synthetic WebSocket frame exceeds fixture size")
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return nil, err
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	for i := range data {
		data[i] ^= mask[i%4]
	}
	return data, nil
}
