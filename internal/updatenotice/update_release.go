package updatenotice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	apiRepository    = "https://api.github.com/repos/openai/openai-cli"
	workflowIdentity = "https://github.com/openai/openai-cli/.github/workflows/publish-release.yml@refs/heads/main"
	maxArchiveBytes  = 128 << 20
	maxBinaryBytes   = 256 << 20
)

var errUpdate = errors.New("automatic update unavailable")
var errWithdrawn = errors.New("prepared release is no longer eligible")

type releaseSelection struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	ReleaseID int64  `json:"release_id"`
	AssetID   int64  `json:"asset_id"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Updated   string `json:"updated"`
}

func archiveName(version string) string {
	platform, extension, arch := runtime.GOOS, ".tar.gz", runtime.GOARCH
	if platform == "darwin" {
		platform, extension = "macos", ".zip"
	}
	if arch == "arm" {
		arch = "armv6"
	}
	return "openai_" + version + "_" + platform + "_" + arch + extension
}

func (s releaseSelection) valid() bool {
	v, ok := stableVersion(s.Version)
	_, err := time.Parse(time.RFC3339, s.Updated)
	return ok && v == s.Version && validCommit(s.Commit) && s.ReleaseID > 0 && s.AssetID > 0 && validDigest(s.Digest) && s.Size > 0 && s.Size <= maxArchiveBytes && err == nil
}

type releaseDelivery struct {
	client   *http.Client
	verifier string
}

func updateClient() *http.Client {
	return &http.Client{
		Timeout: workerTimeout,
		Transport: &http.Transport{
			Proxy:                  http.ProxyFromEnvironment,
			DialContext:            (&net.Dialer{Timeout: 10 * time.Second, FallbackDelay: 50 * time.Millisecond}).DialContext,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  10 * time.Second,
			MaxResponseHeaderBytes: 32 * 1024,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			// Only artifact downloads redirect. Metadata, tags, and attestations
			// stay on their fixed API origin. Never forward SDK credentials.
			if len(via) > 3 || via[0].URL.Host != "github.com" || request.URL.Scheme != "https" || request.URL.Host != "release-assets.githubusercontent.com" || request.URL.User != nil {
				return errUpdate
			}
			return nil
		},
	}
}

func (d *releaseDelivery) get(ctx context.Context, address string, limit int64, target any) error {
	response, err := d.request(ctx, address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return errUpdate
	}
	return json.Unmarshal(data, target)
}

func (d *releaseDelivery) request(ctx context.Context, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "openai-cli-updater")
	if request.URL.Host == "api.github.com" {
		request.Header.Set("Accept", "application/vnd.github+json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
			return nil, errWithdrawn
		}
		return nil, fmt.Errorf("release request status %d", response.StatusCode)
	}
	return response, nil
}

func (d *releaseDelivery) selectRelease(ctx context.Context, pin string) (releaseSelection, error) {
	endpoint := "/releases/latest"
	if pin != "" {
		endpoint = "/releases/tags/v" + pin
	}
	return d.readRelease(ctx, endpoint)
}

func (d *releaseDelivery) readRelease(ctx context.Context, endpoint string) (releaseSelection, error) {
	var response struct {
		ID         int64  `json:"id"`
		Tag        string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
		Published  string `json:"published_at"`
		Assets     []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Digest  string `json:"digest"`
			Size    int64  `json:"size"`
			Updated string `json:"updated_at"`
		} `json:"assets"`
	}
	if err := d.get(ctx, apiRepository+endpoint, maxReleaseBytes, &response); err != nil {
		return releaseSelection{}, err
	}
	version, ok := stableVersion(response.Tag)
	if !ok || response.Tag != "v"+version || response.Draft == nil || *response.Draft || response.Prerelease == nil || *response.Prerelease {
		return releaseSelection{}, errUpdate
	}
	if _, err := time.Parse(time.RFC3339, response.Published); err != nil {
		return releaseSelection{}, errUpdate
	}
	selected := releaseSelection{Version: version, ReleaseID: response.ID}
	for _, asset := range response.Assets {
		if asset.Name != archiveName(version) {
			continue
		}
		if selected.AssetID != 0 || !strings.HasPrefix(asset.Digest, "sha256:") {
			return releaseSelection{}, errUpdate
		}
		selected.AssetID, selected.Digest, selected.Size, selected.Updated = asset.ID, strings.TrimPrefix(asset.Digest, "sha256:"), asset.Size, asset.Updated
	}
	commit, err := d.tagCommit(ctx, version)
	selected.Commit = commit
	if err != nil || !selected.valid() {
		return releaseSelection{}, errUpdate
	}
	return selected, nil
}

func (d *releaseDelivery) tagCommit(ctx context.Context, version string) (string, error) {
	var response struct{ Object struct{ SHA, Type string } }
	if err := d.get(ctx, apiRepository+"/git/ref/tags/v"+version, 16384, &response); err != nil {
		return "", err
	}
	for depth := 0; depth < 4; depth++ {
		if !validCommit(response.Object.SHA) {
			return "", errUpdate
		}
		if response.Object.Type == "commit" {
			return response.Object.SHA, nil
		}
		if response.Object.Type != "tag" {
			return "", errUpdate
		}
		if err := d.get(ctx, apiRepository+"/git/tags/"+response.Object.SHA, 16384, &response); err != nil {
			return "", err
		}
	}
	return "", errUpdate
}

func (d *releaseDelivery) stillPublished(ctx context.Context, selected releaseSelection) error {
	current, err := d.readRelease(ctx, "/releases/"+strconv.FormatInt(selected.ReleaseID, 10))
	if err != nil {
		if errors.Is(err, errUpdate) {
			return errWithdrawn
		}
		return err
	}
	if current != selected {
		return errWithdrawn
	}
	return nil
}

type verifiedAttestation struct {
	VerificationResult struct {
		Signature struct {
			Certificate struct {
				SubjectAlternativeName string `json:"subjectAlternativeName"`
				SourceRepositoryDigest string `json:"sourceRepositoryDigest"`
				RunInvocationURI       string `json:"runInvocationURI"`
			} `json:"certificate"`
		} `json:"signature"`
		Statement struct {
			Subject []struct {
				Name   string
				Digest struct {
					SHA256 string `json:"sha256"`
				}
			} `json:"subject"`
		} `json:"statement"`
	} `json:"verificationResult"`
}

func (d *releaseDelivery) completedPublication(ctx context.Context, verified []verifiedAttestation, selected releaseSelection) error {
	for _, item := range verified {
		certificate := item.VerificationResult.Signature.Certificate
		if certificate.SubjectAlternativeName != workflowIdentity || !validCommit(certificate.SourceRepositoryDigest) {
			continue
		}
		matched := false
		for _, subject := range item.VerificationResult.Statement.Subject {
			if subject.Name == archiveName(selected.Version) && subject.Digest.SHA256 == selected.Digest {
				matched = true
			}
		}
		if !matched {
			continue
		}
		uri, err := url.Parse(certificate.RunInvocationURI)
		if err != nil || uri.Scheme != "https" || uri.Host != "github.com" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" {
			continue
		}
		parts := strings.Split(uri.Path, "/")
		if len(parts) != 8 || strings.Join(parts[:5], "/") != "/openai/openai-cli/actions/runs" || parts[6] != "attempts" {
			continue
		}
		runID, err := strconv.ParseInt(parts[5], 10, 64)
		attempt, attemptErr := strconv.Atoi(parts[7])
		if err != nil || attemptErr != nil || runID <= 0 || attempt <= 0 {
			continue
		}
		var run struct {
			ID                              int64 `json:"id"`
			Attempt                         int   `json:"run_attempt"`
			Status, Conclusion, Event, Path string
			HeadSHA                         string `json:"head_sha"`
			HeadBranch                      string `json:"head_branch"`
			Repository                      struct {
				FullName string `json:"full_name"`
			}
		}
		endpoint := fmt.Sprintf("%s/actions/runs/%d/attempts/%d", apiRepository, runID, attempt)
		if d.get(ctx, endpoint, maxReleaseBytes, &run) != nil {
			continue
		}
		// GitHub returns both bare paths and paths qualified by the dispatch ref.
		workflowPath := run.Path == ".github/workflows/publish-release.yml" || run.Path == ".github/workflows/publish-release.yml@main"
		if run.ID == runID && run.Attempt == attempt && run.Status == "completed" && run.Conclusion == "success" && run.Event == "workflow_dispatch" && workflowPath && run.HeadBranch == "main" && run.HeadSHA == certificate.SourceRepositoryDigest && run.Repository.FullName == "openai/openai-cli" {
			return nil
		}
	}
	return errUpdate
}
