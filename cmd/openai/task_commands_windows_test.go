//go:build windows

package main

import (
	"os"
	"testing"
)

// Exercise the same aliases as the Unix interruption test through a real
// isolated Windows control event. The numeric Windows status is not prescribed.
func TestMainTaskWindowsAudioInterruptClosesStreams(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []struct{ resource, verb, event, prefix string }{
		{"transcriptions", "transcribe", `{"type":"transcript.text.delta","delta":"Visible task output"}`, "Visible task output"},
		{"speech", "speak", `{"type":"speech.audio.delta","audio":"c3ludGhldGlj"}`, "Type: speech.audio.delta"},
	} {
		var wantCode int
		for index, route := range []string{"audio:" + task.resource + " create", "audio " + task.resource + " create", "audio " + task.verb, task.verb} {
			t.Run(route, func(t *testing.T) {
				flags := readableAudioArgs(t, "audio:"+task.resource)[2:]
				if task.verb == "speak" {
					flags = append(flags, "--stream-format", "sse")
				} else {
					flags = append(flags, "--stream=true")
				}
				args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, taskRouteArgs(route, flags...)...)
				code := runWindowsStreamCancellation(t, binary, args, "/audio/"+task.resource, task.event, task.prefix,
					"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
				if index == 0 {
					wantCode = code
				}
				if code == 0 || code != wantCode {
					t.Fatalf("alias cancellation returned %d; canonical route returned %d", code, wantCode)
				}
			})
		}
	}
}
