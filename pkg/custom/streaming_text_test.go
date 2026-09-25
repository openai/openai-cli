package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const responseStreamOperation = "(resource) responses > (method) create"

func TestTextStreamBrokenPipeDoesNotHideFailureOrCancellation(t *testing.T) {
	pipe := &outputWriteError{errors.New("write: broken pipe")}
	require.True(t, isOutputBrokenPipe(pipe))
	require.True(t, isOutputBrokenPipe(fmt.Errorf("wrapped: %w", pipe)))
	require.True(t, isOutputBrokenPipe(errors.Join(pipe, pipe)))
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("the streamed response failed"), errors.New("upstream broken pipe")} {
		require.False(t, isOutputBrokenPipe(errors.Join(pipe, cause)))
		require.False(t, isOutputBrokenPipe(fmt.Errorf("wrapped: %w", errors.Join(pipe, cause))))
	}
}

func TestTextStreamFailureSurvivesOutputErrorsAcrossFormats(t *testing.T) {
	failure := errors.New("synthetic broken pipe")
	for _, opts := range []ShowJSONOpts{
		{Format: "text"}, {Format: "text", RawOutput: true},
		{Format: "text", Transform: "response.error.message"},
		{Format: "text", Transform: "response.error.message", RawOutput: true},
		{Format: "json", ExplicitFormat: true}, {Format: "jsonl", ExplicitFormat: true},
		{Format: "raw", ExplicitFormat: true},
	} {
		opts.Operation, opts.OutputKind = responseStreamOperation, OutputStreamEvent
		for _, processStdout := range []bool{false, true} {
			func() {
				opts.Stdout = failOutputWriter{failure}
				wantSink := failure
				if processStdout {
					file := outputFile(t)
					require.NoError(t, file.Close())
					previous := os.Stdout
					os.Stdout, opts.Stdout = file, file
					defer func() { os.Stdout = previous }()
					wantSink = os.ErrClosed
				}
				iter := streamItems(`{"type":"response.failed","response":{"status":"failed","error":{"message":"synthetic \\u001b message"}}}`, `{"unread":true}`)
				err := ShowJSONIterator(iter, -1, opts)
				require.ErrorIs(t, err, wantSink)
				require.Contains(t, err.Error(), "the streamed response failed")
				require.NotContains(t, err.Error(), "message")
				require.False(t, isOutputBrokenPipe(err))
				require.Equal(t, 1, iter.calls)
			}()
		}
	}
}

func TestTextStreamFailurePresentationWithJoinedCauses(t *testing.T) {
	const private = "synthetic-private-diagnostic https://secret.invalid/?token=fake\x1b[2J"
	iter := streamItems(`{"type":"response.failed","response":{"error":{"message":"synthetic-private-response"}}}`)
	failure := ShowJSONIterator(iter, -1, ShowJSONOpts{
		Operation: responseStreamOperation, OutputKind: OutputStreamEvent,
		Stdout: failOutputWriter{errors.New(private)},
	})
	require.Error(t, failure)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"joined output failure", failure, "the streamed response failed\nOutput may be incomplete."},
		{"wrapped failure", fmt.Errorf("%s: %w", private, failure), "the streamed response failed\nOutput may be incomplete."},
		{"canceled", errors.Join(failure, context.Canceled), "Request canceled."},
		{"deadline", errors.Join(failure, context.DeadlineExceeded), "The request timed out. The API may have received it; check its status before repeating it."},
		{"untyped lookalike", errors.New("the streamed response failed\n" + private), "The command could not be completed. Check your arguments with --help."},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				root := readableErrorTestCommand(t, "--format-error", format)
				var diagnostic bytes.Buffer
				require.NoError(t, ShowCommandError(root, tc.err, &diagnostic))
				message := strings.TrimSpace(diagnostic.String())
				if format == "json" {
					var payload struct {
						Message string `json:"message"`
					}
					require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &payload))
					message = payload.Message
				}
				require.Equal(t, tc.want, message)
				assertReadableErrorContainsNoPrivateDetails(t, diagnostic.String())
			})
		}
	}
}

func TestTextStreamNoPagerOnTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("stream pager regression requires a terminal stdout")
	}
	t.Setenv("PAGER", "/nonexistent-openai-stream-pager")
	text := strings.Repeat("Synthetic text\\n", 45)
	for _, format := range []string{"auto", "text"} {
		iter := streamItems(`{"type":"response.output_text.delta","delta":"`+text+`"}`, `{"type":"response.completed","response":{"status":"completed","output":[]}}`)
		require.NoError(t, ShowJSONIterator(iter, -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Format: format}))
	}
}

func TestTextStreamExplorePreloadFailureOnTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("explorer failure regression requires a terminal stdout")
	}
	iter := streamItems(`{"type":"response.output_text.delta","delta":"Synthetic partial"}`, `{"type":"response.failed","response":{"status":"failed","error":{"message":"Synthetic failure detail"}}}`, `{"unread":true}`)
	err := ShowJSONIterator(iter, -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Format: "explore", ExplicitFormat: true})
	require.EqualError(t, err, "the streamed response failed")
	require.Equal(t, 2, iter.calls)
}

func TestTextStreamInterleavedDetailsKeepPartIdentity(t *testing.T) {
	iter := streamItems(
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Answer A"}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"Answer B"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"item_a","text":"Answer A","annotations":[{"type":"future","note":"belongs to A"}]}`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
	)
	var out bytes.Buffer
	require.NoError(t, ShowJSONIterator(iter, -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Stdout: &out}))
	require.Equal(t, 1, strings.Count(out.String(), "Answer A"))
	require.Contains(t, out.String(), "Answer B\n\nOutput index: 0\nContent index: 0\nItem ID: item_a\nAnnotations:")
	require.Contains(t, out.String(), "belongs to A")
}

func streamItems(values ...string) *transformTestIterator {
	iter := &transformTestIterator{}
	for _, value := range values {
		iter.items = append(iter.items, outputJSON{gjson.Parse(value)})
	}
	return iter
}

func TestTextStreamFailureEventPreservesOutputAndFormat(t *testing.T) {
	const failure = `{"type":"response.failed","response":{"id":"resp_fake","status":"failed","error":{"message":"synthetic failure detail","code":"synthetic"}},"future":9007199254740993}`
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			iter := streamItems(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Partial answer"}`, failure, `{"must_not_consume":true}`)
			err := ShowJSONIterator(iter, -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Format: format, ExplicitFormat: true, Stdout: &out, Stderr: io.Discard})
			require.EqualError(t, err, "the streamed response failed")
			require.Contains(t, out.String(), "Partial answer")
			require.Contains(t, out.String(), "synthetic failure detail")
			require.Contains(t, out.String(), "9007199254740993")
			require.NotContains(t, out.String(), "must_not_consume")
			require.Equal(t, 2, iter.calls)
		})
	}
}

func TestTextStreamFailureIsDetectedBeforeTransformationAndExtraction(t *testing.T) {
	for _, raw := range []bool{false, true} {
		var out bytes.Buffer
		iter := streamItems(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`)
		err := showJSONIterator(iter, -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Format: "text", RawOutput: raw, Transform: "response.status", Stdout: &out}, transformers.Select)
		require.EqualError(t, err, "the streamed response is incomplete")
		require.Equal(t, "incomplete\n", out.String())
	}
	var out bytes.Buffer
	err := showJSONIterator(streamItems(`{"type":"response.failed"}`), -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Stdout: &out}, func(transformers.Route) transformers.Transformer {
		return func(context.Context, gjson.Result) (gjson.Result, error) {
			return gjson.Parse(`{"visible":"transformed"}`), nil
		}
	})
	require.EqualError(t, err, "the streamed response failed")
	require.Contains(t, out.String(), "transformed")
}

func TestTextStreamStopsWithoutConsumingOnLimitAndWriterFailure(t *testing.T) {
	for _, limit := range []int64{0, 1} {
		var out bytes.Buffer
		iter := streamItems(`{"type":"response.output_text.delta","delta":"one"}`, `{"type":"response.failed"}`)
		require.NoError(t, ShowJSONIterator(iter, limit, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Stdout: &out}))
		require.Equal(t, int(limit), iter.calls)
	}
	for _, failure := range []error{errors.New("synthetic sink error"), nil} {
		iter := streamItems(`{"type":"response.output_text.delta","delta":"one"}`, `{"type":"response.failed"}`)
		err := ShowJSONIterator(iter, -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Stdout: failOutputWriter{failure}})
		if failure == nil {
			failure = io.ErrShortWrite
		}
		require.ErrorIs(t, err, failure)
		require.Equal(t, 1, iter.calls)
	}
}

func TestTextStreamPreservesSourceErrorAndContextCause(t *testing.T) {
	upstream := errors.New("synthetic upstream error")
	iter := streamItems(`{"type":"response.output_text.delta","delta":"Partial"}`)
	iter.err = upstream
	var out bytes.Buffer
	err := ShowJSONIterator(iter, -1, ShowJSONOpts{Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Stdout: &out})
	require.ErrorIs(t, err, upstream)
	require.Equal(t, "Partial\n", out.String())
	for _, deadline := range []bool{false, true} {
		var ctx context.Context
		var cancel context.CancelFunc
		want := context.Canceled
		if deadline {
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			ctx, cancel = context.WithCancel(t.Context())
		}
		cancel()
		iter := streamItems(`{"type":"response.output_text.delta","delta":"unread"}`)
		out.Reset()
		err := ShowJSONIterator(iter, -1, ShowJSONOpts{Context: ctx, Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Stdout: &out})
		require.ErrorIs(t, err, want)
		require.Zero(t, iter.calls)
		require.Empty(t, out.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	iter = streamItems(`{"type":"response.output_text.delta","delta":"Partial"}`, `{"type":"response.failed"}`)
	w := &cancelOutputWriter{cancel: cancel}
	err = ShowJSONIterator(iter, -1, ShowJSONOpts{Context: ctx, Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Stdout: w})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, iter.calls)
	require.Equal(t, 1, w.writes)
}
