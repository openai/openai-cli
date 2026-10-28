package batches

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserGuidedMetadataRejectsInvalidValuesBeforeReview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-metadata-key"))
	for _, source := range []string{"local", "uploaded"} {
		for _, raw := range []string{
			`{"note":null}`, `{"note":null,"note":"ok"}`, `{"note":"ok","note":null}`,
			`{"note":false}`, `{"note":123}`, `{"note":["value"]}`, `{"note":{}}`, `null`, `[]`, `{} {}`,
		} {
			t.Run(source+"/"+raw, func(t *testing.T) {
				draft := newBatchBrowserDraft()
				draft.metadata = raw
				if source == "local" {
					draft.path = "requests.jsonl"
				} else {
					draft.inputID = "file_synthetic"
				}
				show := func(_ context.Context, screen *batchBrowserScreen) error {
					require.Equal(t, "Create batch", screen.title, "invalid metadata must stop before preparation or confirmation")
					screen.chosen = "review"
					return nil
				}
				_, err := (workflow{}).createBatchInBrowser(t.Context(), &cli.Command{}, client, nil, draft, show)
				require.ErrorContains(t, err, "Metadata must be a JSON object with text values")
			})
		}
	}
}

func TestBatchBrowserGuidedMetadataPreservesStrings(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{" \t", ""}, {"{}", "{}"},
		{`{"é":"","message":"\u8cc7料\ntext","note":"first","note":"last"}`, `{"é":"","message":"資料\ntext","note":"last"}`},
	} {
		draft := newBatchBrowserDraft()
		draft.metadata = tc.raw
		body, err := draft.body()
		require.NoError(t, err)
		got := gjson.GetBytes(body, "metadata")
		if tc.want == "" {
			require.False(t, got.Exists())
		} else {
			require.JSONEq(t, tc.want, got.Raw)
		}
	}
}

func TestBatchBrowserGuidedNoticeSurvivesLongFolder(t *testing.T) {
	m := newBatchBrowserFlow("picker", "Choose a JSONL input file", strings.Repeat("/long-synthetic-folder", 18), []batchBrowserChoice{
		{value: "path", title: "Enter a file or folder path"},
	})
	m.width, m.height = 50, 12
	m.notice = "Could not read that location. Check the path and access, or choose another file."
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Could not read") {
		t.Fatalf("long folder hid the recovery notice:\n%s", view)
	}
	if !strings.Contains(view, "Esc back") || !strings.Contains(view, "Enter a file") {
		t.Fatalf("notice removed the recovery controls:\n%s", view)
	}
}

func TestBatchBrowserGuidedLocalFailurePreservesSafeRecovery(t *testing.T) {
	failure := (requestconfig.FileInputSource{Option: "--file"}).Failure("open", &os.PathError{
		Op: "open", Path: "/synthetic-private-folder/input.jsonl", Err: os.ErrPermission,
	})
	w := workflow{options: testOptions()}
	err := w.browserSubmissionError(&cli.Command{}, batchSubmission{Stage: "preparation"}, failure)
	text := err.Error()
	for _, want := range []string{"No batch was created", "open", "file", "permissions"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in recovery: %s", want, text)
		}
	}
	if strings.Contains(text, "synthetic-private-folder") {
		t.Errorf("recovery exposed the private local path: %s", text)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatal("recovery lost the original permission failure")
	}
}

func TestBatchBrowserGuidedReviewRequiresExplicitConfirmation(t *testing.T) {
	for _, size := range []struct{ width, height int }{{39, 12}, {40, 11}, {40, 12}} {
		m := newBatchBrowserFlow("confirm", "Review batch", "", nil)
		m.width, m.height = size.width, size.height
		m.flow.lines = []string{"API charges may apply."}
		for range 3 {
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		if m.closed || m.chosen != "" {
			t.Fatalf("Enter confirmed at %dx%d", size.width, size.height)
		}
		m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		want := size.width >= 40 && size.height >= 12
		if (m.closed && m.chosen == "confirm") != want {
			t.Errorf("Ctrl+S confirmation at %dx%d: closed=%v chosen=%q", size.width, size.height, m.closed, m.chosen)
		}
	}
}

func TestBatchBrowserGuidedReviewMatchesProjectHeader(t *testing.T) {
	for _, test := range []struct {
		name, project, environmentHeaders, literalHeader, want string
	}{
		{name: "environment header", environmentHeaders: "OpenAI-Project: proj_environment_header", want: "proj_environment_header"},
		{name: "last environment header", environmentHeaders: "OpenAI-Project: proj_first\nopenai-project: proj_last", want: "proj_last"},
		{name: "explicit project", project: "proj_flag", environmentHeaders: "OpenAI-Project: proj_environment_header", want: "proj_flag"},
		{name: "literal header", project: "proj_flag", environmentHeaders: "OpenAI-Project: proj_environment_header", literalHeader: "OpenAI-Project: proj_literal", want: "proj_literal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OPENAI_PROJECT_ID", "")
			t.Setenv("OPENAI_CUSTOM_HEADERS", test.environmentHeaders)
			t.Setenv("OPENAI_API_KEY", "synthetic-review-key")
			received := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("OpenAI-Project")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
			}))
			defer server.Close()
			cmd := &cli.Command{
				Name: "review", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "project"}, &cli.StringFlag{Name: "base-url"}, requestconfig.NewRequestHeaderFlag()},
				Action: func(ctx context.Context, command *cli.Command) error {
					headers, err := requestconfig.Headers(command)
					if err != nil {
						return err
					}
					opts := append(requestconfig.DefaultOptions(command, "test"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
					for name, values := range headers {
						opts = append(opts, option.WithHeader(name, values[len(values)-1]))
					}
					client := openai.NewClient(opts...)
					if _, err := client.Batches.List(ctx, openai.BatchListParams{}); err != nil {
						return err
					}
					if got := <-received; got != test.want {
						t.Fatalf("actual project = %q, want %q", got, test.want)
					}
					view := strings.Join(batchBrowserReview(command, &batchBrowserDraft{inputID: "file_synthetic", endpoint: "/v1/responses"}, nil), "\n")
					if !strings.Contains(view, "Project: "+test.want+"\n") {
						t.Errorf("review disagrees with the request project:\n%s", view)
					}
					return nil
				},
			}
			args := []string{"review"}
			if test.project != "" {
				args = append(args, "--project", test.project)
			}
			if test.literalHeader != "" {
				args = append(args, "--header", test.literalHeader)
			}
			if err := cmd.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBatchBrowserGuidedReviewOmitsConnectionSecrets(t *testing.T) {
	t.Setenv("OPENAI_PROJECT_ID", "")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	cmd := &cli.Command{Name: "review", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "project"}, &cli.StringFlag{Name: "base-url"}, requestconfig.NewRequestHeaderFlag()},
		Action: func(_ context.Context, command *cli.Command) error {
			view := strings.Join(batchBrowserReview(command, &batchBrowserDraft{inputID: "file_synthetic", endpoint: "/v1/responses"}, nil), "\n")
			if !strings.Contains(view, "Connection: https://gateway.example.invalid") {
				t.Errorf("connection origin missing: %s", view)
			}
			for _, secret := range []string{"synthetic-user", "synthetic-password", "private-path", "synthetic-query", "synthetic-authorization"} {
				if strings.Contains(view, secret) {
					t.Errorf("review contains %q", secret)
				}
			}
			return nil
		},
	}
	err := cmd.Run(context.Background(), []string{"review", "--base-url", "https://synthetic-user:synthetic-password@gateway.example.invalid/private-path?key=synthetic-query", "--header", "Authorization: Bearer synthetic-authorization"})
	if err != nil {
		t.Fatal(err)
	}
}
