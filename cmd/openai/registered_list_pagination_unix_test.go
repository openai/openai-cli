//go:build !windows

package main

import "testing"

func TestMainRegisteredListPagination(t *testing.T) {
	webhooks := resourceListRoute{"webhooks", "/webhook_endpoints", "webhook_endpoint",
		[]string{"webhooks", "list"}, []string{"ID", "NAME", "EVENTS"}}
	skills := resourceListRoute{"skills", "/skills", "skill", []string{"skills", "list"},
		[]string{"ID", "NAME", "DEFAULT VERSION", "LATEST VERSION"}}
	groups := resourceListRoute{"groups", "/organization/groups", "group", []string{"admin", "organization", "groups", "list"},
		[]string{"ID", "NAME", "GROUP TYPE", "IS SCIM MANAGED"}}
	for _, tc := range []struct {
		route        resourceListRoute
		mode, action string
		code         int
	}{
		{webhooks, "normal", "paging", 0},
		{skills, "normal", "paging", 0},
		{groups, "normal", "paging", 0},
		{groups, "repeat-cursor", "error-next", 1},
		{groups, "blocked-next", "q", 0},
		{skills, "error-next", "error-next", 1},
	} {
		t.Run(tc.route.name+"/"+tc.mode, func(t *testing.T) {
			fixture := newResourceListFixture(t, tc.route, tc.mode)
			runResourceListObserver(t, map[string]any{
				"endpoint": fixture.server.URL, "home": t.TempDir(),
				"args": resourceListArgs(tc.route, "--format-error", "json"), "headers": tc.route.headers,
				"mode": tc.mode, "action": tc.action, "expected": tc.code,
			})
			fixture.assertCursors(t, tc.route, 2)
		})
	}
	for _, tc := range []struct {
		route        resourceListRoute
		mode, action string
		flags        []string
		code         int
	}{
		{skills, "empty-continuation", "error", nil, 1},
		{webhooks, "empty-continuation", "error", nil, 1},
		{groups, "missing-cursor", "error", nil, 1},
		{skills, "missing-cursor", "error", nil, 1},
		{groups, "missing-cursor", "one", []string{"--max-items", "1"}, 0},
	} {
		t.Run(tc.route.name+"/"+tc.mode+"/"+tc.action, func(t *testing.T) {
			fixture := newResourceListFixture(t, tc.route, tc.mode)
			runResourceListObserver(t, map[string]any{
				"endpoint": fixture.server.URL, "home": t.TempDir(),
				"args": resourceListArgs(tc.route, append(tc.flags, "--format-error", "json")...), "headers": tc.route.headers,
				"mode": tc.mode, "action": tc.action, "expected": tc.code,
			})
			fixture.assertCursors(t, tc.route, 1)
		})
	}
}
