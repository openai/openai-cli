package clihelp

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestFileInputHelpPreservesPurpose(t *testing.T) {
	for _, tc := range []struct{ name, usage, brief, full string }{
		{"transcription", "The audio file object (not file name) to transcribe, in these formats: mp3, wav. Maximum size 10 MiB.", "Path to the audio file to transcribe, in these formats: mp3, wav.", "Path to the audio file to transcribe, in these formats: mp3, wav. Maximum size 10 MiB."},
		{"translation", "The audio file object (not file name) translate, in these formats: mp3, wav. Maximum size 10 MiB.", "Path to the audio file to translate, in these formats: mp3, wav.", "Path to the audio file to translate, in these formats: mp3, wav. Maximum size 10 MiB."},
		{"voice sample", "The sample audio recording file. Maximum size is 10 MiB.", "The sample audio recording file.", "The sample audio recording file. Maximum size is 10 MiB."},
		{"provenance", "The image or audio file to check for supported OpenAI provenance signals.", "The image or audio file to check for supported OpenAI provenance signals.", "The image or audio file to check for supported OpenAI provenance signals."},
		{"upload", "The File object (not file name) to be uploaded. Maximum size 10 MiB.", "Path to the file to upload.", "Path to the file to upload. Maximum size 10 MiB."},
		{"existing path", "Path to the image to edit. Use PNG or JPEG.", "Path to the image to edit.", "Path to the image to edit. Use PNG or JPEG."},
		{"upload part", "The chunk of bytes for this Part.", "The chunk of bytes for this Part.", "The chunk of bytes for this Part."},
		{"skill files", "Skill files to upload (directory upload) or a single zip file.", "Skill files to upload (directory upload) or a single zip file.", "Skill files to upload (directory upload) or a single zip file."},
	} {
		for _, fileInput := range []bool{false, true} {
			suffix := "/non-file"
			if fileInput {
				suffix = "/file"
			}
			t.Run(tc.name+suffix, func(t *testing.T) {
				flag := &requestflag.Flag[string]{Name: "file", Usage: tc.usage, FileInput: fileInput, Required: true}
				if err := flag.PreParse(); err != nil {
					t.Fatal(err)
				}
				before := flag.String()
				brief := briefHelpAtWidth(&cli.Command{Name: "create", Flags: []cli.Flag{flag}}, "openai", "test create", 200)
				full := fullFlag(flag)
				wantBrief, wantFull := tc.brief, tc.full
				if !fileInput {
					wantBrief = shortDescription(tc.usage)
					wantFull = tc.usage
				}
				if !strings.Contains(brief, wantBrief) {
					t.Errorf("brief help lost file purpose; want %q in:\n%s", wantBrief, brief)
				}
				if !strings.Contains(full, wantFull) {
					t.Errorf("full help lost file purpose or constraints; want %q in:\n%s", wantFull, full)
				}
				if flag.Usage != tc.usage || flag.String() != before {
					t.Fatal("help changed the generated flag definition")
				}
			})
		}
	}
}
