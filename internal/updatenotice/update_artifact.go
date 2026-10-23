package updatenotice

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (d *releaseDelivery) prepare(ctx context.Context, cache *noticeCache, selected releaseSelection) (string, error) {
	address := "https://github.com/openai/openai-cli/releases/download/v" + selected.Version + "/" + archiveName(selected.Version)
	response, err := d.request(ctx, address)
	if err != nil {
		return "", err
	}
	archive, err := cache.root.OpenFile("archive", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		response.Body.Close()
		return "", err
	}
	defer archive.Close()
	defer cache.root.Remove("archive")
	written, copyErr := io.Copy(archive, io.LimitReader(contextReader{ctx, response.Body}, selected.Size+1))
	response.Body.Close()
	if copyErr != nil || written != selected.Size {
		return "", errUpdate
	}
	digest, err := fileDigest(ctx, archive)
	if err != nil || digest != selected.Digest {
		return "", errUpdate
	}
	if err := d.authenticate(ctx, cache, selected); err != nil {
		return "", err
	}
	digest, err = fileDigest(ctx, archive)
	if err != nil || digest != selected.Digest {
		return "", errUpdate
	}
	if !cache.validLock() || !cache.unchangedState() {
		return "", errCache
	}
	// Decode only the authenticated archive. Nothing from it executes here.
	next, err := cache.root.OpenFile("next.tmp", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer next.Close()
	defer cache.root.Remove("next.tmp")
	if err := extractUpdate(ctx, archive, next); err != nil {
		return "", err
	}
	if !binaryIdentity(next, selected.Version, selected.Commit) {
		return "", errUpdate
	}
	digest, err = fileDigest(ctx, next)
	if err != nil {
		return "", err
	}
	if err := next.Chmod(0700); err != nil {
		return "", err
	}
	if err := next.Sync(); err != nil {
		return "", err
	}
	if !cache.validLock() || !cache.unchangedState() {
		return "", errCache
	}
	if err := cache.root.Rename("next.tmp", "next"); err != nil {
		return "", err
	}
	return digest, nil
}

func extractUpdate(ctx context.Context, archive, next *os.File) error {
	info, err := archive.Stat()
	if err != nil {
		return err
	}
	found := false
	copyBinary := func(reader io.Reader, size int64) error {
		if found || size <= 0 || size > maxBinaryBytes {
			return errUpdate
		}
		found = true
		written, err := io.Copy(next, io.LimitReader(contextReader{ctx, reader}, size+1))
		if err != nil || written != size {
			return errUpdate
		}
		return nil
	}
	if runtime.GOOS == "darwin" {
		reader, err := zip.NewReader(archive, info.Size())
		if err != nil {
			return err
		}
		if len(reader.File) > 4096 {
			return errUpdate
		}
		for _, entry := range reader.File {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.Name != "openai" {
				continue
			}
			if !entry.Mode().IsRegular() || entry.UncompressedSize64 > maxBinaryBytes {
				return errUpdate
			}
			content, err := entry.Open()
			if err != nil {
				return err
			}
			err = copyBinary(content, int64(entry.UncompressedSize64))
			content.Close()
			if err != nil {
				return err
			}
		}
	} else {
		compressed, err := gzip.NewReader(io.NewSectionReader(archive, 0, info.Size()))
		if err != nil {
			return err
		}
		defer compressed.Close()
		// This bounds release-archive expansion, not API response payloads.
		reader := tar.NewReader(io.LimitReader(contextReader{ctx, compressed}, maxBinaryBytes+32<<20))
		for count := 0; ; count++ {
			entry, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if count >= 4096 {
				return errUpdate
			}
			if entry.Name != "openai" {
				continue
			}
			if entry.Typeflag != tar.TypeReg {
				return errUpdate
			}
			if err := copyBinary(reader, entry.Size); err != nil {
				return err
			}
		}
	}
	if !found {
		return errUpdate
	}
	return nil
}

func (d *releaseDelivery) authenticate(ctx context.Context, cache *noticeCache, selected releaseSelection) error {
	var response struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := d.get(ctx, apiRepository+"/attestations/sha256:"+selected.Digest, 4<<20, &response); err != nil {
		return err
	}
	if len(response.Attestations) == 0 || len(response.Attestations) > 30 {
		return errUpdate
	}
	bundle, err := cache.root.OpenFile("bundle", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer cache.root.Remove("bundle")
	for _, entry := range response.Attestations {
		if _, err := bundle.Write(append(entry.Bundle, '\n')); err != nil {
			bundle.Close()
			return err
		}
	}
	if err := bundle.Close(); err != nil {
		return err
	}
	command := d.verifierCommand(ctx, cache, "attestation", "verify", filepath.Join(cache.directory, "archive"),
		"--bundle", filepath.Join(cache.directory, "bundle"), "--hostname", "github.com", "--repo", "openai/openai-cli",
		"--cert-identity", workflowIdentity, "--source-ref", "refs/heads/main", "--deny-self-hosted-runners", "--format", "json")
	var output limitedVerification
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return errUpdate
	}
	var verified []verifiedAttestation
	if output.exceeded || json.Unmarshal(output.Bytes(), &verified) != nil {
		return errUpdate
	}
	// Rehash the exact descriptor after the verifier accessed its pathname.
	archive, err := openUpdateFile(cache.root, "archive")
	if err != nil {
		return err
	}
	defer archive.Close()
	digest, err := fileDigest(ctx, archive)
	if err != nil || digest != selected.Digest {
		return errUpdate
	}
	return d.completedPublication(ctx, verified, selected)
}

func (d *releaseDelivery) verifierCommand(ctx context.Context, cache *noticeCache, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, d.verifier, args...)
	command.Env = append(updateEnvironment(), "GH_CONFIG_DIR="+filepath.Join(cache.directory, "gh-config"), "GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1", "GH_PROMPT_DISABLED=1")
	command.WaitDelay = time.Second
	isolateVerifier(command)
	return command
}

func (d *releaseDelivery) checkVerifier(ctx context.Context, cache *noticeCache) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := d.verifierCommand(ctx, cache, "attestation", "verify", "--help")
	var output limitedVerification
	command.Stdout = &output
	if err := command.Run(); err != nil || output.exceeded {
		return errUpdate
	}
	flags := make(map[string]bool)
	for _, field := range strings.Fields(output.String()) {
		flags[field] = true
	}
	for _, flag := range []string{"--bundle", "--hostname", "--repo", "--cert-identity", "--source-ref", "--deny-self-hosted-runners", "--format"} {
		if !flags[flag] {
			return errUpdate
		}
	}
	return nil
}

type limitedVerification struct {
	bytes.Buffer
	exceeded bool
}

func (b *limitedVerification) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 1<<20 {
		b.exceeded = true
		return 0, errUpdate
	}
	return b.Buffer.Write(data)
}

func updateEnvironment() []string {
	result := []string{}
	// Keep tool lookup and trusted host TLS/proxy settings. Exclude API keys,
	// GitHub tokens, debug settings, and arbitrary process injection variables.
	for _, key := range []string{"HOME", "PATH", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy", "OPENAI_CLI_NO_UPDATE_CHECK", "OPENAI_CLI_NO_AUTO_UPDATE", "OPENAI_CLI_VERSION_PIN"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	return append(result, "LC_ALL=C", "LANG=C")
}

// Keep this package's existing notice behavior for installations whose owner
// must perform upgrades. Pins suppress conflicting manual notices as well.
func CheckOrUpdate(ctx context.Context, current string) string {
	if message, managed := Automatic(ctx, current); managed {
		return message
	}
	if strings.TrimSpace(os.Getenv("OPENAI_CLI_VERSION_PIN")) != "" {
		return ""
	}
	return Check(ctx, current)
}
