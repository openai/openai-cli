package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const projectLinkAliasPage = `{"object":"list","data":[],"has_more":false}`

type projectLinkAliasStatus struct {
	Directory        string `json:"directory"`
	LinkedDirectory  string `json:"linked_directory"`
	Project          string `json:"project"`
	EffectiveProject string `json:"effective_project"`
	Inherited        bool   `json:"inherited"`
}

// Canonicalize temporary ancestors before creating or storing fixture paths.
// Capability checks use the actual volume, including per-directory behavior.
func projectLinkNativeAliases(t *testing.T, first, second string) (string, string) {
	t.Helper()
	parent := projectLinkCanonicalDirectory(t, t.TempDir())
	left, right := filepath.Join(parent, first), filepath.Join(parent, second)
	require.NoError(t, os.Mkdir(left, 0o700))
	rightInfo, err := os.Stat(right)
	if os.IsNotExist(err) {
		t.Skipf("fixture volume does not resolve %q and %q as aliases", first, second)
	}
	require.NoError(t, err)
	leftInfo, err := os.Stat(left)
	require.NoError(t, err)
	require.True(t, os.SameFile(leftInfo, rightInfo), "alias fixture must identify one actual directory")
	return left, right
}

func projectLinkDistinctFoldDirectories(t *testing.T) (string, string) {
	t.Helper()
	for _, pair := range [][2]string{{"Work", "work"}, {"\u1c90", "\u10d0"}, {"\ua7c0", "\ua7c1"}} {
		if !strings.EqualFold(pair[0], pair[1]) {
			continue
		}
		parent := projectLinkCanonicalDirectory(t, t.TempDir())
		left, right := filepath.Join(parent, pair[0]), filepath.Join(parent, pair[1])
		require.NoError(t, os.Mkdir(left, 0o700))
		err := os.Mkdir(right, 0o700)
		if os.IsExist(err) {
			leftInfo, leftErr := os.Stat(left)
			rightInfo, rightErr := os.Stat(right)
			require.NoError(t, leftErr)
			require.NoError(t, rightErr)
			require.True(t, os.SameFile(leftInfo, rightInfo))
			continue
		}
		require.NoError(t, err)
		leftInfo, err := os.Stat(left)
		require.NoError(t, err)
		rightInfo, err := os.Stat(right)
		require.NoError(t, err)
		require.False(t, os.SameFile(leftInfo, rightInfo), "exclusive directory creation must produce distinct entries")
		t.Logf("filesystem distinguishes EqualFold names %q and %q", pair[0], pair[1])
		return left, right
	}
	t.Skip("fixture volume exposes no distinct directories for the tested EqualFold name pairs")
	return "", ""
}

func projectLinkAliasRun(t *testing.T, home, directory string, env []string, args ...string) mainDispatchResult {
	t.Helper()
	env = append(slices.Clone(env), "PWD="+directory)
	return runProjectLinkMain(t, home, directory, env, "", args...)
}

func projectLinkAliasInspect(t *testing.T, home, directory string) projectLinkAliasStatus {
	t.Helper()
	got := projectLinkAliasRun(t, home, directory, nil, "link", "--format=json")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	var status projectLinkAliasStatus
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &status))
	return status
}

func projectLinkAliasSeed(t *testing.T, home string, links map[string]string) []byte {
	t.Helper()
	path := projectLinkRegistryPath(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	data, err := json.MarshalIndent(links, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return data
}

func projectLinkAliasUnchanged(t *testing.T, home string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(projectLinkRegistryPath(home))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func projectLinkAliasRequest(t *testing.T, home, directory string, env []string, requests <-chan globalFlagsRequest, want []string, flags ...string) {
	t.Helper()
	args := append([]string{"files", "list", "--format=raw"}, flags...)
	got := projectLinkAliasRun(t, home, directory, env, args...)
	require.Equal(t, mainDispatchResult{stdout: projectLinkAliasPage + "\n"}, got)
	request := globalFlagsOneRequest(t, requests)
	require.Equal(t, http.MethodGet, request.method)
	require.Equal(t, "/files", request.path)
	require.Equal(t, want, request.header.Values("OpenAI-Project"))
	require.Equal(t, "Bearer synthetic-env-key", request.header.Get("Authorization"))
}

func TestMainProjectLinkCaseAliases(t *testing.T) {
	for _, saveAlias := range []bool{false, true} {
		t.Run(map[bool]string{false: "save original", true: "save alias"}[saveAlias], func(t *testing.T) {
			original, alias := projectLinkNativeAliases(t, "Work", "work")
			home := t.TempDir()
			saved, other := original, alias
			if saveAlias {
				saved, other = alias, original
			}
			got := projectLinkAliasRun(t, home, saved, nil, "link", "--project=proj_case")
			require.Equal(t, 0, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			server, requests := globalFlagsServer(t, projectLinkAliasPage)
			for _, directory := range []string{saved, other} {
				status := projectLinkAliasInspect(t, home, directory)
				require.Equal(t, "proj_case", status.Project)
				require.Equal(t, "proj_case", status.EffectiveProject)
				require.Equal(t, status.Directory, status.LinkedDirectory)
				require.False(t, status.Inherited)
				projectLinkAliasRequest(t, home, directory, globalFlagsEnv(server), requests, []string{"proj_case"})
			}
			got = projectLinkAliasRun(t, home, other, nil, "unlink")
			require.Equal(t, 0, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Empty(t, projectLinkRegistry(t, home))
			require.Empty(t, projectLinkAliasInspect(t, home, saved).Project)
			projectLinkAliasRequest(t, home, saved, globalFlagsEnv(server), requests, nil)
		})
	}
}

func TestMainProjectLinkLegacyAndDuplicateAliases(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy spelling", true: "same-project duplicates"}[duplicate], func(t *testing.T) {
			original, alias := projectLinkNativeAliases(t, "Work", "work")
			home := t.TempDir()
			links := map[string]string{alias: "proj_legacy"}
			if duplicate {
				links[original] = "proj_legacy"
			}
			before := projectLinkAliasSeed(t, home, links)
			server, requests := globalFlagsServer(t, projectLinkAliasPage)
			status := projectLinkAliasInspect(t, home, original)
			require.Equal(t, "proj_legacy", status.Project)
			require.Equal(t, status.Directory, status.LinkedDirectory)
			require.False(t, status.Inherited)
			projectLinkAliasRequest(t, home, original, globalFlagsEnv(server), requests, []string{"proj_legacy"})
			projectLinkAliasUnchanged(t, home, before)
			got := projectLinkAliasRun(t, home, original, nil, "link", "--project=proj_updated")
			require.Equal(t, 0, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			updated := projectLinkRegistry(t, home)
			require.Len(t, updated, 1)
			for key, project := range updated {
				require.Contains(t, links, key, "replacement must retain an existing key")
				require.Equal(t, "proj_updated", project)
			}
			if !duplicate {
				require.Equal(t, "proj_updated", updated[alias])
			} else {
				require.Equal(t, "proj_updated", updated[projectLinkCanonicalDirectory(t, original)])
			}
			got = projectLinkAliasRun(t, home, alias, nil, "unlink")
			require.Equal(t, 0, got.code, "%s", got.stderr)
			require.Empty(t, projectLinkRegistry(t, home))
		})
	}
}

func TestMainProjectLinkAliasConflictsAndExplicitRepair(t *testing.T) {
	original, alias := projectLinkNativeAliases(t, "Work", "work")
	home := t.TempDir()
	unrelated := projectLinkCanonicalDirectory(t, t.TempDir())
	links := map[string]string{original: "proj_original", alias: "proj_alias", unrelated: "proj_unrelated"}
	before := projectLinkAliasSeed(t, home, links)
	server, requests := globalFlagsServer(t, projectLinkAliasPage)
	for _, directory := range []string{original, alias} {
		for _, command := range [][]string{{"link"}, {"files", "list"}} {
			got := projectLinkAliasRun(t, home, directory, globalFlagsEnv(server), command...)
			require.Equal(t, 1, got.code)
			require.Empty(t, got.stdout)
			require.Contains(t, got.stderr, "different projects to the same folder")
			require.Contains(t, got.stderr, "This command did not change saved links.")
			require.NotContains(t, got.stderr, home)
			projectLinkAliasUnchanged(t, home, before)
			require.Empty(t, requests)
		}
	}
	for _, override := range []struct {
		flags, env, want []string
	}{
		{[]string{"--project=proj_flag"}, nil, []string{"proj_flag"}},
		{[]string{"--project="}, nil, []string{""}},
		{nil, []string{"OPENAI_PROJECT_ID=proj_environment"}, []string{"proj_environment"}},
		{nil, []string{"OPENAI_PROJECT_ID="}, []string{""}},
		{[]string{"--header=OpenAI-Project: proj_header"}, nil, []string{"proj_header"}},
		{[]string{"--header=OpenAI-Project:"}, nil, []string{""}},
	} {
		projectLinkAliasRequest(t, home, alias, globalFlagsEnv(server, override.env...), requests, override.want, override.flags...)
		projectLinkAliasUnchanged(t, home, before)
	}
	got := projectLinkAliasRun(t, home, alias, nil, "link", "--project=proj_repaired")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	updated := projectLinkRegistry(t, home)
	require.Len(t, updated, 2)
	require.Equal(t, "proj_unrelated", updated[unrelated])
	require.Equal(t, "proj_repaired", updated[projectLinkCanonicalDirectory(t, alias)])
	require.Equal(t, "proj_repaired", projectLinkAliasInspect(t, home, original).Project)
	projectLinkAliasSeed(t, home, links)
	got = projectLinkAliasRun(t, home, alias, nil, "unlink", "--format=json")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.True(t, json.Valid([]byte(got.stdout)))
	require.Equal(t, map[string]string{unrelated: "proj_unrelated"}, projectLinkRegistry(t, home))
	require.Empty(t, requests)
}

func TestMainProjectLinkAliasNearestParent(t *testing.T) {
	parent, alias := projectLinkNativeAliases(t, "Work", "work")
	child := filepath.Join(parent, "Child")
	grandchild := filepath.Join(child, "Grandchild")
	require.NoError(t, os.MkdirAll(grandchild, 0o700))
	aliasChild := filepath.Join(alias, "Child")
	aliasGrandchild := filepath.Join(aliasChild, "Grandchild")
	home := t.TempDir()
	projectLinkAliasSeed(t, home, map[string]string{filepath.Dir(parent): "proj_outer", parent: "proj_parent"})
	server, requests := globalFlagsServer(t, projectLinkAliasPage)
	status := projectLinkAliasInspect(t, home, aliasGrandchild)
	require.Equal(t, "proj_parent", status.Project)
	require.True(t, status.Inherited)
	require.Equal(t, filepath.Dir(filepath.Dir(status.Directory)), status.LinkedDirectory)
	projectLinkAliasRequest(t, home, aliasGrandchild, globalFlagsEnv(server), requests, []string{"proj_parent"})
	got := projectLinkAliasRun(t, home, aliasChild, nil, "link", "--project=proj_child")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	projectLinkAliasRequest(t, home, grandchild, globalFlagsEnv(server), requests, []string{"proj_child"})
	got = projectLinkAliasRun(t, home, child, nil, "unlink")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	projectLinkAliasRequest(t, home, aliasGrandchild, globalFlagsEnv(server), requests, []string{"proj_parent"})
}

func TestMainProjectLinkAliasUnlinkCommitsBeforeInheritedConflict(t *testing.T) {
	parent, alias := projectLinkNativeAliases(t, "Work", "work")
	child, aliasChild := filepath.Join(parent, "Child"), filepath.Join(alias, "Child")
	require.NoError(t, os.Mkdir(child, 0o700))
	home := t.TempDir()
	parents := map[string]string{parent: "proj_parent_first", alias: "proj_parent_second"}
	projectLinkAliasSeed(t, home, map[string]string{parent: parents[parent], alias: parents[alias], child: "proj_child", aliasChild: "proj_child"})
	server, requests := globalFlagsServer(t, projectLinkAliasPage)
	projectLinkAliasRequest(t, home, child, globalFlagsEnv(server), requests, []string{"proj_child"})
	got := projectLinkAliasRun(t, home, aliasChild, globalFlagsEnv(server), "unlink", "--format-error=json")
	require.Equal(t, 1, got.code)
	require.Empty(t, got.stdout)
	message, ok := decodeMainStructuredError(t, "json", got.stderr)["message"].(string)
	require.True(t, ok)
	require.Contains(t, message, "This folder has no saved link of its own.")
	require.Contains(t, message, "Its inherited project could not be resolved.")
	require.Contains(t, message, "different projects to the same folder")
	require.NotContains(t, message, "did not change saved links")
	require.Equal(t, parents, projectLinkRegistry(t, home))
	require.Empty(t, requests, "committed local unlink must not contact the API")
}

func TestMainProjectLinkNormalizationAliases(t *testing.T) {
	nfc, nfd := projectLinkNativeAliases(t, "Caf\u00e9", "Cafe\u0301")
	home := t.TempDir()
	before := projectLinkAliasSeed(t, home, map[string]string{nfd: "proj_normalized"})
	status := projectLinkAliasInspect(t, home, nfc)
	require.Equal(t, "proj_normalized", status.Project)
	require.Equal(t, status.Directory, status.LinkedDirectory)
	require.False(t, status.Inherited)
	server, requests := globalFlagsServer(t, projectLinkAliasPage)
	projectLinkAliasRequest(t, home, nfc, globalFlagsEnv(server), requests, []string{"proj_normalized"})
	projectLinkAliasUnchanged(t, home, before)
	got := projectLinkAliasRun(t, home, nfc, nil, "unlink")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	require.Empty(t, projectLinkRegistry(t, home))
}

func TestMainProjectLinkDistinctCaseFoldDirectories(t *testing.T) {
	left, right := projectLinkDistinctFoldDirectories(t)
	home := t.TempDir()
	for directory, project := range map[string]string{left: "proj_left", right: "proj_right"} {
		got := projectLinkAliasRun(t, home, directory, nil, "link", "--project", project)
		require.Equal(t, 0, got.code, "%s", got.stderr)
	}
	server, requests := globalFlagsServer(t, projectLinkAliasPage)
	projectLinkAliasRequest(t, home, left, globalFlagsEnv(server), requests, []string{"proj_left"})
	projectLinkAliasRequest(t, home, right, globalFlagsEnv(server), requests, []string{"proj_right"})
	require.Len(t, projectLinkRegistry(t, home), 2)
	got := projectLinkAliasRun(t, home, left, nil, "unlink")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	projectLinkAliasRequest(t, home, right, globalFlagsEnv(server), requests, []string{"proj_right"})
	require.Equal(t, map[string]string{right: "proj_right"}, projectLinkRegistry(t, home))
}

func TestMainProjectLinkRetargetedAliasDoesNotMoveSavedLink(t *testing.T) {
	left, right := projectLinkDistinctFoldDirectories(t)
	leftChild, rightChild := filepath.Join(left, "Child"), filepath.Join(right, "Child")
	require.NoError(t, os.Mkdir(leftChild, 0o700))
	require.NoError(t, os.Mkdir(rightChild, 0o700))
	home := t.TempDir()
	before := projectLinkAliasSeed(t, home, map[string]string{leftChild: "proj_stale"})
	require.NoError(t, os.Rename(left, filepath.Join(filepath.Dir(left), "Moved")))
	if err := os.Symlink(right, left); err != nil {
		t.Skipf("fixture cannot create the native directory symlink: %v", err)
	}
	leftInfo, err := os.Stat(leftChild)
	require.NoError(t, err)
	rightInfo, err := os.Stat(rightChild)
	require.NoError(t, err)
	require.True(t, os.SameFile(leftInfo, rightInfo), "redirected leaves must identify the same object")
	require.Empty(t, projectLinkAliasInspect(t, home, rightChild).Project)
	server, requests := globalFlagsServer(t, projectLinkAliasPage)
	projectLinkAliasRequest(t, home, rightChild, globalFlagsEnv(server), requests, nil)
	projectLinkAliasUnchanged(t, home, before)
	got := projectLinkAliasRun(t, home, rightChild, nil, "link", "--project=proj_target")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	require.Equal(t, map[string]string{leftChild: "proj_stale", rightChild: "proj_target"}, projectLinkRegistry(t, home))
	got = projectLinkAliasRun(t, home, rightChild, nil, "unlink")
	require.Equal(t, 0, got.code, "%s", got.stderr)
	require.Equal(t, map[string]string{leftChild: "proj_stale"}, projectLinkRegistry(t, home))
}
