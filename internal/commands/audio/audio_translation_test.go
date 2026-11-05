package audio

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
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestLiveTranslationSetupErrorsAreUsefulAndPrivate(t *testing.T) {
	for status, message := range map[int]string{400: "rejected the live translation settings", 401: "could not verify your access", 403: "API key permissions", 404: "API address", 429: "API usage limit", 503: "Try again later"} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, `{"error":{"message":"synthetic-private-diagnostic","code":"synthetic"}}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err := connectAudioTranslation(ctx, audiolive.Config{Language: "en"}, []option.RequestOption{option.WithAPIKey("synthetic-test-key"), option.WithBaseURL(server.URL + "/v1/"), option.WithMaxRetries(0)})
			require.ErrorContains(t, err, message)
			require.NotContains(t, err.Error(), "synthetic-private-diagnostic")
			require.NotContains(t, err.Error(), "synthetic-test-key")
		})
	}
}

func TestLiveTranslationAuthenticationCauseNeedsEvidence(t *testing.T) {
	require.Contains(t, translationFailure(401, "", nil).Error(), "could not verify your access")
	require.NotContains(t, translationFailure(401, "", nil).Error(), "key was not accepted")
	require.Contains(t, translationFailure(0, "invalid_api_key", nil).Error(), "key was not accepted")
}

func TestLiveTranslationPreservesFileRouteAndRejectsMixedModes(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		message   string
		delegated bool
	}{
		{[]string{"--file", "sample.wav", "--model", "whisper-1"}, "", true},
		{[]string{"--live", "--file", "sample.wav"}, "Use --live by itself", false},
		{[]string{"--live", "--model", "whisper-1"}, "Use --live by itself", false},
		{[]string{"--live", "extra"}, "does not accept positional", false},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			called := false
			translate := &cli.Command{Name: "translate", Flags: []cli.Flag{&cli.StringFlag{Name: "file"}, &cli.StringFlag{Name: "model"}}, Action: func(context.Context, *cli.Command) error { called = true; return nil }}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{{Name: "audio", Commands: []*cli.Command{translate}}}}
			ConfigureLiveTranslation(root, TranslationOptions{DefaultRequestOptions: func(*cli.Command) []option.RequestOption { t.Fatal("unexpected request preparation"); return nil }, RequestOptions: func(*cli.Command) ([]option.RequestOption, error) { t.Fatal("unexpected API options"); return nil, nil }})
			err := root.Run(t.Context(), append([]string{"openai", "audio", "translate"}, tc.args...))
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.message)
			}
			require.Equal(t, tc.delegated, called)
		})
	}
}

// The fixture implements only the RFC6455 frames used by this synthetic exchange.
// It uses no browser, external service, helper installation, or real credential.
func translationFixtureFrames(conn net.Conn, reader *bufio.Reader, data []byte) ([]byte, error) {
	if data != nil {
		header := []byte{0x81}
		if len(data) < 126 {
			header = append(header, byte(len(data)))
		} else if len(data) < 65536 {
			header = append(header, 126, byte(len(data)>>8), byte(len(data)))
		} else {
			header = append(header, 127)
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(data)))
			header = append(header, length[:]...)
		}
		if _, err := conn.Write(append(header, data...)); err != nil {
			return nil, err
		}
		return nil, nil
	}
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		var b [2]byte
		if _, err := io.ReadFull(reader, b[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	}
	if length == 127 {
		var b [8]byte
		if _, err := io.ReadFull(reader, b[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	var mask [4]byte
	if header[1]&0x80 != 0 {
		if _, err := io.ReadFull(reader, mask[:]); err != nil {
			return nil, err
		}
	}
	data = make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	if header[1]&0x80 != 0 {
		for i := range data {
			data[i] ^= mask[i%4]
		}
	}
	return data, nil
}

func TestLiveTranslationSDKRoundTripPreservesConfigurationAudioAndDrain(t *testing.T) {
	observed := make(chan []byte, 3)
	serverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime/translations" || r.URL.Query().Get("model") != "gpt-realtime-translate" || r.Header.Get("Authorization") != "Bearer synthetic-test-key" {
			http.Error(w, "wrong synthetic request", 400)
			return
		}
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		accept := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
		buffer.Flush()
		send := func(data string) error {
			_, err := translationFixtureFrames(conn, buffer.Reader, []byte(data))
			return err
		}
		if err = send(`{"type":"session.created"}`); err != nil {
			serverDone <- err
			return
		}
		config, err := translationFixtureFrames(conn, buffer.Reader, nil)
		if err != nil {
			serverDone <- err
			return
		}
		observed <- config
		if err = send(`{"type":"session.updated"}`); err != nil {
			serverDone <- err
			return
		}
		audio, err := translationFixtureFrames(conn, buffer.Reader, nil)
		if err != nil {
			serverDone <- err
			return
		}
		observed <- audio
		for _, event := range []string{`{"type":"session.input_transcript.delta","delta":"Hola."}`, `{"type":"session.output_audio.delta","delta":"AQIDBA==","sample_rate":48000,"channels":2,"format":"pcm16"}`} {
			if err = send(event); err != nil {
				serverDone <- err
				return
			}
		}
		closeEvent, err := translationFixtureFrames(conn, buffer.Reader, nil)
		if err != nil {
			serverDone <- err
			return
		}
		observed <- closeEvent
		for _, event := range []string{`{"type":"session.output_transcript.delta","delta":"Hello."}`, `{"type":"session.closed"}`} {
			if err = send(event); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := connectAudioTranslation(ctx, audiolive.Config{Language: "fr", NoiseReduction: true}, []option.RequestOption{option.WithAPIKey("synthetic-test-key"), option.WithBaseURL(server.URL + "/v1/"), option.WithMaxRetries(0)})
	require.NoError(t, err)
	defer session.Abort()
	var config map[string]any
	require.NoError(t, json.Unmarshal(<-observed, &config))
	audioConfig := config["session"].(map[string]any)["audio"].(map[string]any)
	require.Equal(t, map[string]any{"language": "fr"}, audioConfig["output"])
	input := audioConfig["input"].(map[string]any)
	require.NotContains(t, input, "format")
	require.Equal(t, map[string]any{"model": "gpt-realtime-whisper"}, input["transcription"])
	require.Equal(t, map[string]any{"type": "near_field"}, input["noise_reduction"])
	pcm := bytes.Repeat([]byte{0x12, 0x34}, 40000)
	require.NoError(t, session.Append(ctx, pcm))
	var appendEvent struct{ Type, Audio string }
	require.NoError(t, json.Unmarshal(<-observed, &appendEvent))
	require.Equal(t, "session.input_audio_buffer.append", appendEvent.Type)
	decoded, err := base64.StdEncoding.DecodeString(appendEvent.Audio)
	require.NoError(t, err)
	require.Equal(t, pcm, decoded)
	event, err := session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hola.", event.Delta)
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, 48000, event.SampleRate)
	require.Equal(t, 2, event.Channels)
	require.NoError(t, session.FinishInput(ctx))
	require.JSONEq(t, `{"type":"session.close"}`, string(<-observed))
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello.", event.Delta)
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "session.closed", event.Type)
	require.NoError(t, <-serverDone)
}
