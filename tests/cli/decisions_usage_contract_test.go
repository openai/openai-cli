package cli_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestDecisionsUsageRepeatedFilters(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai").CombinedOutput(); err != nil {
		t.Fatalf("building CLI: %v\n%s", err, output)
	}
	for _, repeated := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "repeated"}[repeated], func(t *testing.T) {
			received := make(chan url.Values, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/organization/usage/decisions" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				received <- r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"page","data":[],"has_more":false,"next_page":null}`)
			}))
			t.Cleanup(server.Close)
			args := []string{"--base-url", server.URL, "--api-key", "fake-project-key", "--admin-api-key", "fake-admin-key", "admin:organization:usage", "decisions", "--start-time", "100", "--batch", "false"}
			filters := []struct{ flag, query, first, second string }{
				{"api-key-id", "api_key_ids[]", "key_a", "key_b"},
				{"project-id", "project_ids[]", "proj_a", "proj_b"},
				{"user-id", "user_ids[]", "user_a", "user_b"},
				{"model", "models[]", "model_a", "model_b"},
				{"group-by", "group_by[]", "project_id", "api_source"},
			}
			if repeated {
				for _, filter := range filters {
					args = append(args, "--"+filter.flag, filter.first, "--"+filter.flag, filter.second)
				}
			}
			command := exec.CommandContext(t.Context(), binary, args...)
			command.Env = []string{"FORCE_COLOR=0"}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("running CLI: %v\n%s", err, output)
			}
			select {
			case query := <-received:
				if query.Get("start_time") != "100" || query.Get("batch") != "false" {
					t.Errorf("scalar query values were changed: %v", query)
				}
				for _, filter := range filters {
					var expected []string
					if repeated {
						expected = []string{filter.first, filter.second}
					}
					if !slices.Equal(query[filter.query], expected) {
						t.Errorf("%s = %v, want %v", filter.query, query[filter.query], expected)
					}
				}
			default:
				t.Fatal("CLI sent no request")
			}
		})
	}
}
