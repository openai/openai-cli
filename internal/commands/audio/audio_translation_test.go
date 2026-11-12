package audio

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/modeldefaults"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestLiveTranslationSetupErrorsAreUsefulAndPrivate(t *testing.T) {
	for status, message := range map[int]string{200: "WebSocket upgrade", 400: "rejected the live translation settings", 401: "could not verify your access", 403: "API key permissions", 404: "API address", 407: "proxy requires authentication", 426: "WebSocket upgrade", 429: "API usage limit", 503: "Try again later"} {
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

func TestTranslationShowsCredentialGuidanceAfterRejection(t *testing.T) {
	const setup = "Run openai help setup."
	policy := TranslationOptions{
		AuthenticationGuidance: func(_ *cli.Command, failure *openai.Error) AuthenticationGuidance {
			require.Equal(t, http.StatusUnauthorized, failure.StatusCode)
			return AuthenticationGuidance{Summary: "No API key is configured.", Action: setup}
		},
	}
	failure := translationFailure(401, "", nil)
	require.EqualError(t, policy.explainAccess(nil, failure, false), "No API key is configured.\n"+setup)
	require.Equal(t, failure, policy.explainAccess(nil, failure, true))
	refused := translationFailure(0, "", syscall.ECONNREFUSED)
	require.Equal(t, refused, policy.explainAccess(nil, refused, false))
}

func TestTranslationTransportFailuresHidePrivateDetails(t *testing.T) {
	const private = "private-proxy.synthetic.invalid"
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{Err: "not found", Name: private}, "could not resolve"},
		{&tls.CertificateVerificationError{Err: x509.HostnameError{Certificate: &x509.Certificate{}, Host: private}}, "certificate could not be verified"},
		{&net.OpError{Op: "dial", Net: private, Err: syscall.ECONNREFUSED}, "connection was refused"},
		{context.DeadlineExceeded, "timed out"},
	} {
		err := translationFailure(0, "", fmt.Errorf("private-diagnostic: %w", tc.err))
		require.ErrorContains(t, err, tc.want)
		require.NotContains(t, err.Error(), private)
		require.NotContains(t, err.Error(), "private-diagnostic")
	}
}

func TestTranslationReportsInterruptedSetupAfterUpgrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := upgradeTranslationFixture(w, r)
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := connectAudioTranslation(ctx, audiolive.Config{Language: "en"}, []option.RequestOption{option.WithBaseURL(server.URL + "/v1/"), option.WithAPIKey("synthetic-test-key")})
	require.ErrorContains(t, err, "connection opened, but session setup did not finish")
}

func TestTranslationSendsNoSettingsUntilSessionCreated(t *testing.T) {
	for _, firstEvent := range []string{
		`{"type":"error","error":{"type":"server_error","code":null}}`,
		`{"type":"session.updated"}`,
	} {
		serverDone := make(chan error, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, reader, err := upgradeTranslationFixture(w, r)
			if err != nil {
				serverDone <- err
				return
			}
			defer conn.Close()
			if _, err := translationFixtureFrames(conn, reader, []byte(firstEvent)); err != nil {
				serverDone <- err
				return
			}
			// The client must abort without sending session.update.
			frame, err := reader.ReadByte()
			switch {
			case err == io.EOF, err == nil && frame&0xf == 8:
				serverDone <- nil
			case err != nil:
				serverDone <- err
			default:
				serverDone <- fmt.Errorf("received client frame before session.created: opcode %d", frame&0xf)
			}
		}))
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		session, err := connectAudioTranslation(ctx, audiolive.Config{Language: "hi"}, []option.RequestOption{
			option.WithAPIKey("synthetic-test-key"), option.WithBaseURL(server.URL + "/v1/"),
		})
		if session != nil {
			session.Abort()
		}
		server.Close()
		require.Error(t, err)
		require.NotErrorIs(t, err, context.DeadlineExceeded)
		select {
		case serverErr := <-serverDone:
			require.NoError(t, serverErr)
		case <-ctx.Done():
			t.Fatal("the fixture did not observe the client disconnect")
		}
		cancel()
	}
}

func TestTranslationEndpointsDoNotSendAnAdminKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("admin credential reached a translation endpoint")
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key"}}`)
	}))
	defer server.Close()
	options := []option.RequestOption{option.WithBaseURL(server.URL + "/v1/"), option.WithAdminAPIKey("synthetic-admin"), option.WithMaxRetries(0)}
	_, err := connectAudioTranslation(t.Context(), audiolive.Config{Language: "en"}, options)
	require.ErrorContains(t, err, "verify your access")
	_, err = translateTypedText(t.Context(), "Hello", "English", "", options)
	require.Error(t, err)
}

func TestTypedTranslationRequestAndResults(t *testing.T) {
	const input = "  @synthetic.txt\nTranslate as written.  "
	const completed = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":" traduit "},{"type":"output_text","text":"ici."}]}],"usage":{"input_tokens":0,"output_tokens":3}}`
	form := newTerminalTranslationModel(t.Context(), nil, &cli.Command{}, nil, nil)
	require.Equal(t, modeldefaults.TranslationText, form.form.textModel)
	for _, test := range []struct {
		name, model, body string
		wantError         bool
	}{
		{"selected model", "my-text-model", completed, false},
		{"default form", form.form.textModel, completed, false},
		{"default fallback", "", completed, false},
		{"incomplete", "", `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"private partial text"}]}]}`, true},
		{"empty", "", `{"status":"completed","output":[]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertBody := map[string]any{}
				if err := json.NewDecoder(r.Body).Decode(&assertBody); err != nil {
					t.Error(err)
				}
				expectedModel := test.model
				if expectedModel == "" {
					expectedModel = modeldefaults.TranslationText
				}
				if r.Method != "POST" || r.URL.Path != "/v1/responses" || assertBody["input"] != input || assertBody["model"] != expectedModel ||
					assertBody["store"] != false || !strings.Contains(fmt.Sprint(assertBody["instructions"]), "French") {
					t.Error("unexpected text translation request")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			out, err := translateTypedText(t.Context(), input, "French", test.model,
				[]option.RequestOption{option.WithAPIKey("synthetic-test-key"), option.WithBaseURL(server.URL + "/v1/"), option.WithMaxRetries(0)})
			if test.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private partial text")
				require.Empty(t, out.Text)
				return
			}
			require.NoError(t, err)
			require.Equal(t, " traduit ici.", out.Text)
			require.NotNil(t, out.InputTokens)
			require.NotNil(t, out.OutputTokens)
			require.Equal(t, int64(0), *out.InputTokens)
			require.Equal(t, int64(3), *out.OutputTokens)
		})
	}
}

func TestLiveTranslationRejectsInvalidModesBeforeCredentials(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"translate-live", "--browser", "--no-playback"}, "requires terminal translation"},
		{[]string{"translate-live", "--browser", "--file", "sample.wav"}, "requires terminal translation"},
		{[]string{"translate-live", "--file", "sample.wav", "--input-device", "0"}, "Choose one audio input"},
		{[]string{"translate-live", "--target-language", "invalid"}, "supported target language"},
		{[]string{"translate-live", "--output-dir", ""}, "folder for saving"},
		{[]string{"translate-live", "extra"}, "does not accept positional"},
		{[]string{"translate", "--live", "--file", "sample.wav"}, "requires terminal translation"},
		{[]string{"translate", "--browser"}, "Use audio translate-live --browser"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			translate := &cli.Command{
				Name:  "translate",
				Flags: []cli.Flag{&cli.StringFlag{Name: "file"}},
				Action: func(context.Context, *cli.Command) error {
					t.Fatal("invalid mode reached the file translation action")
					return nil
				},
			}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{{Name: "audio", Commands: []*cli.Command{translate}}}}
			ConfigureLiveTranslation(root, TranslationOptions{
				DefaultRequestOptions: func(*cli.Command) []option.RequestOption { t.Fatal("credentials accessed"); return nil },
				RequestOptions:        func(*cli.Command) ([]option.RequestOption, error) { t.Fatal("credentials accessed"); return nil, nil },
			})
			err := root.Run(t.Context(), append([]string{"openai", "audio"}, tc.args...))
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func upgradeTranslationFixture(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.Reader, error) {
	conn, buffer, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	accept := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
	if err := buffer.Flush(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, buffer.Reader, nil
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
	defaults := audiolive.Config{Language: "fr", NoiseReduction: true}
	t.Run("default models", func(t *testing.T) {
		testTranslationRoundTrip(t, defaults, modeldefaults.Translation, modeldefaults.TranslationTranscript)
	})
	t.Run("Hindi with default settings", func(t *testing.T) {
		testTranslationRoundTrip(t, audiolive.Config{Language: "hi"}, modeldefaults.Translation, modeldefaults.TranslationTranscript)
	})
	t.Run("selected models", func(t *testing.T) {
		config := defaults
		config.Model = "test-translation-model"
		config.TranscriptionModel = "test-transcription-model"
		testTranslationRoundTrip(t, config, config.Model, config.TranscriptionModel)
	})
}

func testTranslationRoundTrip(t *testing.T, selection audiolive.Config, translationModel, transcriptionModel string) {
	t.Helper()
	observed := make(chan []byte, 3)
	serverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime/translations" || r.URL.Query().Get("model") != translationModel || r.Header.Get("Authorization") != "Bearer synthetic-test-key" {
			http.Error(w, "wrong synthetic request", 400)
			return
		}
		conn, reader, err := upgradeTranslationFixture(w, r)
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		send := func(data string) error {
			_, err := translationFixtureFrames(conn, reader, []byte(data))
			return err
		}
		if err = send(`{"type":"session.created"}`); err != nil {
			serverDone <- err
			return
		}
		config, err := translationFixtureFrames(conn, reader, nil)
		if err != nil {
			serverDone <- err
			return
		}
		observed <- config
		if err = send(`{"type":"session.updated"}`); err != nil {
			serverDone <- err
			return
		}
		audio, err := translationFixtureFrames(conn, reader, nil)
		if err != nil {
			serverDone <- err
			return
		}
		observed <- audio
		for _, event := range []string{
			`{"type":"conversation.item.input_audio_transcription.delta","delta":"Hola.","item_id":"a","content_index":2,"elapsed_ms":0}`,
			`{"type":"input_audio_buffer.transcription.done","transcript":"Hola, mundo.","item_id":"a","content_index":2,"elapsed_ms":120}`,
			`{"type":"conversation.item.input_audio_transcription.completed","transcript":"","item_id":"b"}`,
			`{"type":"session.output_audio.delta","delta":"AQIDBA==","sample_rate":48000,"channels":2,"format":"pcm16","elapsed_ms":400}`,
		} {
			if err = send(event); err != nil {
				serverDone <- err
				return
			}
		}
		closeEvent, err := translationFixtureFrames(conn, reader, nil)
		if err != nil {
			serverDone <- err
			return
		}
		observed <- closeEvent
		for _, event := range []string{`{"type":"session.output_transcript.delta","delta":"Hello.","elapsed_ms":400}`, `{"type":"session.usage","usage":{"input_tokens":12,"output_tokens":0,"total_tokens":12}}`, `{"type":"session.closed"}`} {
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
	session, err := connectAudioTranslation(ctx, selection, []option.RequestOption{option.WithAPIKey("synthetic-test-key"), option.WithBaseURL(server.URL + "/v1/"), option.WithMaxRetries(0)})
	require.NoError(t, err)
	defer session.Abort()
	var config map[string]any
	require.NoError(t, json.Unmarshal(<-observed, &config))
	audioConfig := config["session"].(map[string]any)["audio"].(map[string]any)
	require.Equal(t, map[string]any{"language": selection.Language}, audioConfig["output"])
	input := audioConfig["input"].(map[string]any)
	require.NotContains(t, input, "format")
	require.Equal(t, map[string]any{"model": transcriptionModel}, input["transcription"])
	if selection.NoiseReduction {
		require.Equal(t, map[string]any{"type": "near_field"}, input["noise_reduction"])
	} else {
		require.Contains(t, input, "noise_reduction")
		require.Nil(t, input["noise_reduction"])
	}
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
	require.Equal(t, "session.input_transcript.delta", event.Type)
	require.NotNil(t, event.ElapsedMS)
	require.Equal(t, float64(0), *event.ElapsedMS)
	require.Equal(t, "a", event.ItemID)
	require.Equal(t, 2, event.ContentIndex)
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "session.input_transcript.completed", event.Type)
	require.Equal(t, "a", event.ItemID)
	require.Equal(t, 2, event.ContentIndex)
	require.Equal(t, "Hola, mundo.", *event.Transcript)
	require.Equal(t, float64(120), *event.ElapsedMS)
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "session.input_transcript.completed", event.Type)
	require.Equal(t, "b", event.ItemID)
	require.Equal(t, "", *event.Transcript)
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, event.ElapsedMS)
	require.Equal(t, float64(400), *event.ElapsedMS)
	require.Equal(t, 48000, event.SampleRate)
	require.Equal(t, 2, event.Channels)
	require.NoError(t, session.FinishInput(ctx))
	require.JSONEq(t, `{"type":"session.close"}`, string(<-observed))
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello.", event.Delta)
	require.NotNil(t, event.ElapsedMS)
	require.Equal(t, float64(400), *event.ElapsedMS)
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "usage", event.Type)
	require.Equal(t, int64(12), event.Tokens)
	require.NotNil(t, event.InputTokens)
	require.Equal(t, int64(12), *event.InputTokens)
	require.NotNil(t, event.OutputTokens)
	require.Zero(t, *event.OutputTokens)
	event, err = session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "session.closed", event.Type)
	require.NoError(t, <-serverDone)
}
