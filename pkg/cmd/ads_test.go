package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdsAccountRetrieveUsesAdsEnvAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/ad_account", r.URL.Path)
		require.Equal(t, "Bearer env-ads-key", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		_, _ = w.Write([]byte(`{"id":"act_123","name":"Acme Ads"}`))
	}))
	defer server.Close()

	output := runOpenAIAds(t, server.URL, map[string]string{"OPENAI_ADS_API_KEY": "env-ads-key"},
		"ads", "account", "retrieve",
	)

	assert.JSONEq(t, `{"id":"act_123","name":"Acme Ads"}`, output)
}

func TestAdsCampaignCreateBuildsNestedJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/campaigns", r.URL.Path)
		require.Equal(t, "Bearer ads-key", r.Header.Get("Authorization"))
		require.Contains(t, r.Header.Get("Content-Type"), "application/json")

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "Spring launch", body["name"])
		assert.Equal(t, "Promote the bundle", body["description"])
		assert.Equal(t, "active", body["status"])
		assert.Equal(t, float64(1735689600), body["start_time"])
		assert.Equal(t, float64(1738368000), body["end_time"])
		assert.Equal(t, map[string]any{"lifetime_spend_limit_micros": float64(25000000)}, body["budget"])
		assert.Equal(t, map[string]any{
			"locations":          map[string]any{"countries": []any{"US", "CA"}},
			"excluded_locations": map[string]any{"countries": []any{"GB"}},
		}, body["targeting"])

		_, _ = w.Write([]byte(`{"id":"cmpn_101","status":"active"}`))
	}))
	defer server.Close()

	output := runOpenAIAds(t, server.URL, nil,
		"--ads-api-key", "ads-key",
		"ads", "campaigns", "create",
		"--name", "Spring launch",
		"--description", "Promote the bundle",
		"--status", "active",
		"--start-time", "1735689600",
		"--end-time", "1738368000",
		"--lifetime-spend-limit-micros", "25000000",
		"--country", "US",
		"--country", "CA",
		"--exclude-country", "GB",
	)

	assert.JSONEq(t, `{"id":"cmpn_101","status":"active"}`, output)
}

func TestAdsInsightsCampaignUsesBracketArrayQueryParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/campaigns/cmpn_101/insights", r.URL.Path)
		query := r.URL.Query()
		assert.Equal(t, "daily", query.Get("time_granularity"))
		assert.Equal(t, "ad", query.Get("aggregation_level"))
		assert.Equal(t, "7", query.Get("limit"))
		assert.Equal(t, []string{"clicks", "impressions"}, query["fields[]"])
		assert.Equal(t, []string{`{"type":"date_range","since":"2026-04-25","until":"2026-05-01"}`}, query["time_ranges[]"])
		assert.Equal(t, []string{`{"field":"clicks","direction":"desc"}`}, query["sort[]"])
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer server.Close()

	runOpenAIAds(t, server.URL, nil,
		"--ads-api-key", "ads-key",
		"ads", "insights", "campaign", "cmpn_101",
		"--time-granularity", "daily",
		"--aggregation-level", "ad",
		"--limit", "7",
		"--field", "clicks",
		"--field", "impressions",
		"--time-range", `{"type":"date_range","since":"2026-04-25","until":"2026-05-01"}`,
		"--sort", `{"field":"clicks","direction":"desc"}`,
	)
}

func TestAdsFilesUploadSupportsMultipartFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "creative.png")
	require.NoError(t, os.WriteFile(filePath, []byte("png bytes"), 0o644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/upload", r.URL.Path)
		require.Contains(t, r.Header.Get("Content-Type"), "multipart/form-data")
		reader, err := r.MultipartReader()
		require.NoError(t, err)
		part, err := reader.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "file", part.FormName())
		assert.Equal(t, "creative.png", part.FileName())
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		assert.Equal(t, "png bytes", string(data))
		_, err = reader.NextPart()
		assert.ErrorIs(t, err, io.EOF)
		_, _ = w.Write([]byte(`{"file_id":"file_901"}`))
	}))
	defer server.Close()

	output := runOpenAIAds(t, server.URL, nil,
		"--ads-api-key", "ads-key",
		"ads", "files", "upload",
		"--file", filePath,
	)

	assert.JSONEq(t, `{"file_id":"file_901"}`, output)
}

func TestAdsRequiresAdsAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent without an Ads API key")
	}))
	defer server.Close()

	_, errOutput, err := runOpenAIAdsRaw(t, server.URL, nil,
		"ads", "account", "retrieve",
	)

	require.Error(t, err)
	assert.Contains(t, errOutput, "OPENAI_ADS_API_KEY")
	assert.Contains(t, errOutput, "--ads-api-key")
}

func runOpenAIAds(t *testing.T, adsBaseURL string, env map[string]string, args ...string) string {
	stdout, stderr, err := runOpenAIAdsRaw(t, adsBaseURL, env, args...)
	require.NoError(t, err, "stderr: %s\nstdout: %s", stderr, stdout)
	return stdout
}

func runOpenAIAdsRaw(t *testing.T, adsBaseURL string, env map[string]string, args ...string) (string, string, error) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..")

	allArgs := append([]string{"run", "./cmd/openai", "--format", "json", "--ads-base-url", adsBaseURL}, args...)
	cmd := exec.Command("go", allArgs...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "OPENAI_ADS_API_KEY=")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Keep multipart imported by generated docs-friendly test failures when net/http
// changes how the boundary is encoded.
var _ = multipart.ErrMessageTooLarge
