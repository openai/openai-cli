//go:build darwin || linux

package updatenotice

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonUpdateResponse(t *testing.T, data any) *http.Response {
	t.Helper()
	body, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}

func releaseJSON(selected releaseSelection) map[string]any {
	return map[string]any{"id": selected.ReleaseID, "tag_name": "v" + selected.Version, "draft": false, "prerelease": false, "published_at": time.Now().UTC().Format(time.RFC3339), "assets": []any{map[string]any{"id": selected.AssetID, "name": archiveName(selected.Version), "digest": "sha256:" + selected.Digest, "size": selected.Size, "updated_at": selected.Updated}}}
}

func TestAutomaticReleaseSelectionAndWithdrawal(t *testing.T) {
	for _, scenario := range []string{"immediate", "draft", "prerelease", "bad_tag", "missing_digest", "wrong_arch", "deleted", "replaced_asset", "moved_tag", "pinned"} {
		t.Run(scenario, func(t *testing.T) {
			selected := syntheticSelection()
			response := releaseJSON(selected)
			switch scenario {
			case "draft":
				response["draft"] = true
			case "prerelease":
				response["prerelease"] = true
			case "bad_tag":
				response["tag_name"] = "v1.0.1-rc1"
			case "missing_digest":
				response["assets"].([]any)[0].(map[string]any)["digest"] = nil
			case "wrong_arch":
				response["assets"].([]any)[0].(map[string]any)["name"] = "other.zip"
			case "replaced_asset":
				response["assets"].([]any)[0].(map[string]any)["id"] = 999
			}
			pin := ""
			if scenario == "pinned" {
				pin = "1.0.1"
			}
			d := releaseDelivery{client: &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "" || request.Header.Get("Api-Key") != "" {
					t.Fatal("credentials in release request")
				}
				if strings.Contains(request.URL.Path, "/git/ref/") {
					commit := selected.Commit
					if scenario == "moved_tag" {
						commit = strings.Repeat("2", 40)
					}
					return jsonUpdateResponse(t, map[string]any{"object": map[string]any{"sha": commit, "type": "commit"}}), nil
				}
				if scenario == "deleted" {
					return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				if scenario == "pinned" && !strings.HasSuffix(request.URL.Path, "/tags/v1.0.1") {
					t.Fatal("pin selected latest")
				}
				return jsonUpdateResponse(t, response), nil
			})}}
			if scenario == "replaced_asset" || scenario == "moved_tag" || scenario == "deleted" {
				if err := d.stillPublished(t.Context(), selected); err == nil {
					t.Fatal("withdrawn or replaced artifact accepted")
				}
				return
			}
			got, err := d.selectRelease(t.Context(), pin)
			if scenario == "immediate" || scenario == "pinned" {
				if err != nil || got != selected {
					t.Fatalf("stable release rejected: %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatal("ineligible release accepted")
			}
		})
	}
}

func TestAutomaticUpdateRedirects(t *testing.T) {
	client := updateClient()
	defer client.CloseIdleConnections()
	for _, target := range []string{"http://release-assets.githubusercontent.com/a", "https://evil.example/a", "https://github.com/other", "https://user:pass@release-assets.githubusercontent.com/a"} {
		uri, _ := url.Parse(target)
		origin, _ := url.Parse("https://github.com/openai/openai-cli/releases/download/v1.0.1/a")
		if client.CheckRedirect(&http.Request{URL: uri}, []*http.Request{{URL: origin}}) == nil {
			t.Fatal("unsafe redirect accepted")
		}
	}
	asset, _ := url.Parse("https://release-assets.githubusercontent.com/a?signature=synthetic")
	origin, _ := url.Parse("https://github.com/openai/openai-cli/releases/download/v1.0.1/a")
	if err := client.CheckRedirect(&http.Request{URL: asset}, []*http.Request{{URL: origin}}); err != nil {
		t.Fatal(err)
	}
	origin, _ = url.Parse(apiRepository + "/releases/latest")
	if client.CheckRedirect(&http.Request{URL: asset}, []*http.Request{{URL: origin}}) == nil {
		t.Fatal("metadata redirected")
	}
}

func updateArchive(t *testing.T, content []byte, duplicate bool) []byte {
	t.Helper()
	var data bytes.Buffer
	count := 1
	if duplicate {
		count = 2
	}
	if runtime.GOOS == "darwin" {
		writer := zip.NewWriter(&data)
		for range count {
			entry, err := writer.Create("openai")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = entry.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&data)
		writer := tar.NewWriter(compressed)
		for range count {
			if err := writer.WriteHeader(&tar.Header{Name: "openai", Mode: 0700, Size: int64(len(content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return data.Bytes()
}

func TestAutomaticUpdateAuthenticatedPreparation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-secret")
	t.Setenv("GH_TOKEN", "synthetic-token")
	t.Setenv("GH_DEBUG", "api")
	binary := buildUpdateFixture(t, t.TempDir(), "1.0.1")
	for _, scenario := range []string{"valid", "bare_workflow_path", "wrong_workflow_ref", "wrong_workflow_path", "digest_mismatch", "truncated", "verifier_failure", "wrong_identity", "wrong_subject", "wrong_run_sha", "incomplete_run", "failed_run", "wrong_attempt", "wrong_version", "duplicate_binary", "oversized_output"} {
		t.Run(scenario, func(t *testing.T) {
			content := binary
			if scenario == "wrong_version" {
				content = buildUpdateFixture(t, t.TempDir(), "9.9.9")
			}
			archive := updateArchive(t, content, scenario == "duplicate_binary")
			selected := syntheticSelection()
			sum := sha256.Sum256(archive)
			selected.Digest = hex.EncodeToString(sum[:])
			selected.Size = int64(len(archive))
			if scenario == "digest_mismatch" {
				selected.Digest = strings.Repeat("f", 64)
			}
			if scenario == "truncated" {
				archive = archive[:len(archive)-1]
			}
			certificate := map[string]any{"subjectAlternativeName": workflowIdentity, "sourceRepositoryDigest": syntheticCommit, "runInvocationURI": "https://github.com/openai/openai-cli/actions/runs/123/attempts/2"}
			if scenario == "wrong_identity" {
				certificate["subjectAlternativeName"] = "https://github.com/other/repo"
			}
			subjectName := archiveName(selected.Version)
			if scenario == "wrong_subject" {
				subjectName = "other.zip"
			}
			verified := []any{map[string]any{"verificationResult": map[string]any{"signature": map[string]any{"certificate": certificate}, "statement": map[string]any{"subject": []any{map[string]any{"name": subjectName, "digest": map[string]any{"sha256": selected.Digest}}}}}}}
			output, err := json.Marshal(verified)
			if err != nil {
				t.Fatal(err)
			}
			verifier := filepath.Join(t.TempDir(), "gh")
			// This fixture validates invocation policy only. The review packet
			// separately records real gh verification of an official archive.
			script := "#!/bin/sh\nset -eu\ntest -z \"${OPENAI_API_KEY-}${GH_TOKEN-}${GH_DEBUG-}\"\ntest \"$1 $2\" = 'attestation verify'\ntest \"$4\" = --bundle\ntest \"$6 $7\" = '--hostname github.com'\ntest \"$8 $9\" = '--repo openai/openai-cli'\ntest \"${10}\" = --cert-identity\ntest \"${11}\" = '" + workflowIdentity + "'\ntest \"${12} ${13}\" = '--source-ref refs/heads/main'\ntest \"${14} ${15} ${16}\" = '--deny-self-hosted-runners --format json'\n"
			if scenario == "verifier_failure" {
				script += "exit 1\n"
			} else if scenario == "oversized_output" {
				script += "/usr/bin/head -c 1100000 /dev/zero\n"
			} else {
				script += "printf '%s' '" + string(output) + "'\n"
			}
			if err := os.WriteFile(verifier, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cache, err := openCache(t.Context(), filepath.Join(t.TempDir(), "update"))
			if err != nil {
				t.Fatal(err)
			}
			defer cache.close()
			d := releaseDelivery{verifier: verifier, client: &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "github.com" {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive))}, nil
				}
				if strings.Contains(request.URL.Path, "/attestations/") {
					return jsonUpdateResponse(t, map[string]any{"attestations": []any{map[string]any{"bundle": map[string]any{"synthetic": true}}}}), nil
				}
				run := map[string]any{"id": 123, "run_attempt": 2, "status": "completed", "conclusion": "success", "event": "workflow_dispatch", "path": ".github/workflows/publish-release.yml@main", "head_sha": syntheticCommit, "head_branch": "main", "repository": map[string]any{"full_name": "openai/openai-cli"}}
				switch scenario {
				case "bare_workflow_path":
					run["path"] = ".github/workflows/publish-release.yml"
				case "wrong_workflow_ref":
					run["path"] = ".github/workflows/publish-release.yml@other"
				case "wrong_workflow_path":
					run["path"] = ".github/workflows/other.yml@main"
				case "wrong_run_sha":
					run["head_sha"] = strings.Repeat("2", 40)
				case "incomplete_run":
					run["status"] = "in_progress"
				case "failed_run":
					run["conclusion"] = "failure"
				case "wrong_attempt":
					run["run_attempt"] = 1
				}
				return jsonUpdateResponse(t, run), nil
			})}}
			digest, err := d.prepare(t.Context(), cache, selected)
			if scenario == "valid" || scenario == "bare_workflow_path" {
				want := sha256.Sum256(binary)
				if err != nil || digest != hex.EncodeToString(want[:]) {
					t.Fatalf("prepare: %s %v", digest, err)
				}
			} else {
				if err == nil {
					t.Fatal("unverified artifact prepared")
				}
				if _, err := cache.root.Stat("next"); !os.IsNotExist(err) {
					t.Fatal("failed verification left an activatable binary")
				}
			}
		})
	}
}

func TestAutomaticUpdateCanceledExtraction(t *testing.T) {
	directory := t.TempDir()
	archive := filepath.Join(directory, "archive")
	next := filepath.Join(directory, "next")
	if err := os.WriteFile(archive, updateArchive(t, bytes.Repeat([]byte("x"), 1<<20), false), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.Create(next)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := extractUpdate(ctx, source, destination); err == nil {
		t.Fatal("canceled extraction succeeded")
	}
}

func TestAutomaticUpdateVerifierCapabilities(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-secret")
	t.Setenv("GH_TOKEN", "synthetic-token")
	t.Setenv("GH_DEBUG", "api")
	const help = "--bundle string\n--hostname string\n--repo string\n--cert-identity string\n--source-ref string\n--deny-self-hosted-runners\n--format string\n"
	for _, missing := range []string{"", "--bundle", "--hostname", "--repo", "--cert-identity", "--source-ref", "--deny-self-hosted-runners", "--format", "command", "oversized", "canceled"} {
		name := missing
		if name == "" {
			name = "supported"
		}
		t.Run(name, func(t *testing.T) {
			cache, err := openCache(t.Context(), filepath.Join(t.TempDir(), "update"))
			if err != nil {
				t.Fatal(err)
			}
			defer cache.close()
			output := help
			if strings.HasPrefix(missing, "--") {
				output = strings.ReplaceAll(output, missing, missing+"-unsupported")
			}
			script := "#!/bin/sh\nset -eu\ntest -z \"${OPENAI_API_KEY-}${GH_TOKEN-}${GH_DEBUG-}\"\ntest \"$*\" = 'attestation verify --help'\n"
			switch missing {
			case "command":
				script += "exit 1\n"
			case "oversized":
				script += "/usr/bin/head -c 1100000 /dev/zero\n"
			default:
				script += "printf '%s' '" + output + "'\n"
			}
			verifier := filepath.Join(t.TempDir(), "gh")
			if err := os.WriteFile(verifier, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			d := releaseDelivery{verifier: verifier}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if missing == "canceled" {
				cancel()
			}
			err = d.checkVerifier(ctx, cache)
			if (err == nil) != (missing == "") {
				t.Fatalf("capability check = %v, missing %q", err, missing)
			}
		})
	}
}

func Example_updatePolicy() {
	pin, enabled := updatePolicy(func(key string) string {
		if key == "OPENAI_CLI_VERSION_PIN" {
			return "v1.40.4"
		}
		return ""
	})
	fmt.Println(pin, enabled)
	// Output: 1.40.4 true
}

func TestAutomaticUpdateVerifierCancellation(t *testing.T) {
	for _, capabilities := range []bool{false, true} {
		name := "verification"
		if capabilities {
			name = "capabilities"
		}
		t.Run(name, func(t *testing.T) { testUpdateVerifierCancellation(t, capabilities) })
	}
}

func testUpdateVerifierCancellation(t *testing.T, capabilities bool) {
	t.Helper()
	archive := updateArchive(t, []byte("synthetic binary never extracted"), false)
	selected := syntheticSelection()
	sum := sha256.Sum256(archive)
	selected.Digest, selected.Size = hex.EncodeToString(sum[:]), int64(len(archive))
	directory := t.TempDir()
	pidPath := filepath.Join(directory, "descendant.pid")
	verifier := filepath.Join(directory, "gh")
	script := "#!/bin/sh\n/bin/sleep 30 &\nprintf '%s' \"$!\" > '" + pidPath + "'\nwait\n"
	if err := os.WriteFile(verifier, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cache, err := openCache(t.Context(), filepath.Join(directory, "update"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	d := releaseDelivery{verifier: verifier, client: &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "github.com" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive))}, nil
		}
		return jsonUpdateResponse(t, map[string]any{"attestations": []any{map[string]any{"bundle": map[string]any{"synthetic": true}}}}), nil
	})}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		if capabilities {
			finished <- d.checkVerifier(ctx, cache)
			return
		}
		_, err := d.prepare(ctx, cache, selected)
		finished <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for pid == 0 {
		data, err := os.ReadFile(pidPath)
		if err == nil {
			pid, _ = strconv.Atoi(string(data))
		}
		if time.Now().After(deadline) {
			t.Fatal("verifier did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !updateTestProcessRunning(t, pid) {
		t.Fatal("verifier descendant exited before cancellation")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled verifier succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("verifier cancellation did not finish")
	}
	deadline = time.Now().Add(3 * time.Second)
	for updateTestProcessRunning(t, pid) {
		if time.Now().After(deadline) {
			t.Fatal("verifier descendant survived cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := cache.root.Stat("next"); !os.IsNotExist(err) {
		t.Fatal("cancellation prepared an executable")
	}
}

func updateTestProcessRunning(t *testing.T, pid int) bool {
	t.Helper()
	if runtime.GOOS == "linux" {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		// The command name can contain spaces and parentheses. The state follows its final closing parenthesis.
		end := strings.LastIndex(string(stat), ") ")
		if end < 0 || len(stat) <= end+2 {
			t.Fatal("invalid descendant process status")
		}
		// A zombie has terminated even when the container's PID 1 has not reaped it.
		return stat[end+2] != 'Z'
	}
	err := syscall.Kill(pid, 0)
	if err == syscall.ESRCH {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}
