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

func TestAdsEndpointCoverage(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		queryKey   string
		queryValue string
		args       []string
	}{
		{name: "account retrieve", method: http.MethodGet, path: "/ad_account", args: []string{"ads", "account", "retrieve"}},
		{name: "campaigns list", method: http.MethodGet, path: "/campaigns", queryKey: "limit", queryValue: "10", args: []string{"ads", "campaigns", "list", "--limit", "10"}},
		{name: "campaigns create", method: http.MethodPost, path: "/campaigns", args: []string{"ads", "campaigns", "create", "--name", "Spring launch", "--status", "active", "--lifetime-spend-limit-micros", "25000000"}},
		{name: "campaigns retrieve", method: http.MethodGet, path: "/campaigns/cmpn_101", args: []string{"ads", "campaigns", "retrieve", "cmpn_101"}},
		{name: "campaigns update", method: http.MethodPost, path: "/campaigns/cmpn_101", args: []string{"ads", "campaigns", "update", "cmpn_101", "--status", "paused"}},
		{name: "campaigns activate", method: http.MethodPost, path: "/campaigns/cmpn_101/activate", args: []string{"ads", "campaigns", "activate", "cmpn_101"}},
		{name: "campaigns pause", method: http.MethodPost, path: "/campaigns/cmpn_101/pause", args: []string{"ads", "campaigns", "pause", "cmpn_101"}},
		{name: "campaigns archive", method: http.MethodPost, path: "/campaigns/cmpn_101/archive", args: []string{"ads", "campaigns", "archive", "cmpn_101"}},
		{name: "ad groups list", method: http.MethodGet, path: "/ad_groups", queryKey: "campaign_id", queryValue: "cmpn_101", args: []string{"ads", "ad-groups", "list", "--campaign-id", "cmpn_101"}},
		{name: "ad groups create", method: http.MethodPost, path: "/ad_groups", args: []string{"ads", "ad-groups", "create", "--campaign-id", "cmpn_101", "--name", "US English", "--status", "active", "--max-bid-micros", "60000"}},
		{name: "ad groups retrieve", method: http.MethodGet, path: "/ad_groups/adgrp_301", args: []string{"ads", "ad-groups", "retrieve", "adgrp_301"}},
		{name: "ad groups update", method: http.MethodPost, path: "/ad_groups/adgrp_301", args: []string{"ads", "ad-groups", "update", "adgrp_301", "--status", "paused"}},
		{name: "ad groups activate", method: http.MethodPost, path: "/ad_groups/adgrp_301/activate", args: []string{"ads", "ad-groups", "activate", "adgrp_301"}},
		{name: "ad groups pause", method: http.MethodPost, path: "/ad_groups/adgrp_301/pause", args: []string{"ads", "ad-groups", "pause", "adgrp_301"}},
		{name: "ad groups archive", method: http.MethodPost, path: "/ad_groups/adgrp_301/archive", args: []string{"ads", "ad-groups", "archive", "adgrp_301"}},
		{name: "ads list", method: http.MethodGet, path: "/ads", queryKey: "ad_group_id", queryValue: "adgrp_301", args: []string{"ads", "ads", "list", "--ad-group-id", "adgrp_301"}},
		{name: "ads create", method: http.MethodPost, path: "/ads", args: []string{"ads", "ads", "create", "--ad-group-id", "adgrp_301", "--name", "Planner launch card", "--status", "active", "--title", "Try planner", "--body", "Coordinate work.", "--target-url", "https://example.com", "--file-id", "file_901"}},
		{name: "ads retrieve", method: http.MethodGet, path: "/ads/ad_501", args: []string{"ads", "ads", "retrieve", "ad_501"}},
		{name: "ads update", method: http.MethodPost, path: "/ads/ad_501", args: []string{"ads", "ads", "update", "ad_501", "--status", "paused"}},
		{name: "ads activate", method: http.MethodPost, path: "/ads/ad_501/activate", args: []string{"ads", "ads", "activate", "ad_501"}},
		{name: "ads pause", method: http.MethodPost, path: "/ads/ad_501/pause", args: []string{"ads", "ads", "pause", "ad_501"}},
		{name: "ads archive", method: http.MethodPost, path: "/ads/ad_501/archive", args: []string{"ads", "ads", "archive", "ad_501"}},
		{name: "files upload url", method: http.MethodPost, path: "/upload", args: []string{"ads", "files", "upload", "--image-url", "https://example.com/card.png"}},
		{name: "insights account", method: http.MethodGet, path: "/ad_account/insights", args: []string{"ads", "insights", "account"}},
		{name: "insights campaign", method: http.MethodGet, path: "/campaigns/cmpn_101/insights", args: []string{"ads", "insights", "campaign", "cmpn_101"}},
		{name: "insights ad group", method: http.MethodGet, path: "/ad_groups/adgrp_301/insights", args: []string{"ads", "insights", "ad-group", "adgrp_301"}},
		{name: "insights ad", method: http.MethodGet, path: "/ads/ad_501/insights", args: []string{"ads", "insights", "ad", "ad_501"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tc.method, r.Method)
				require.Equal(t, tc.path, r.URL.Path)
				require.Equal(t, "Bearer ads-key", r.Header.Get("Authorization"))
				if tc.queryKey != "" {
					assert.Equal(t, tc.queryValue, r.URL.Query().Get(tc.queryKey))
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()

			runOpenAIAds(t, server.URL, nil, append([]string{"--ads-api-key", "ads-key"}, tc.args...)...)
		})
	}
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

	allArgs := append([]string{"run", "./cmd/openai", "--format", "json", "--base-url", adsBaseURL}, args...)
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
