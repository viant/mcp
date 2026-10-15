// SPDX-License-Identifier: Apache-2.0
// MCP client configuration extracted from Viant Agently Core.
package config

import "testing"

func TestResourceCacheScopeExactOverridesAndWildcardDefault(t *testing.T) {
	config := &MCPClient{ResourceCacheScopes: map[string]string{"*": ResourceCacheConversation, "workspace": ResourceCachePublic, "workspace/navigation": ResourceCacheUser}}
	cases := []struct{ namespace, kind, want string }{{"unknown", "window", ResourceCacheConversation}, {"workspace", "window", ResourceCachePublic}, {"workspace", "navigation", ResourceCacheUser}}
	for _, item := range cases {
		got, err := config.ResourceCacheScope(item.namespace, item.kind)
		if err != nil || got != item.want {
			t.Fatalf("scope %s/%s got %q, %v", item.namespace, item.kind, got, err)
		}
	}
	config.ResourceCacheScopes["*"] = "unsupported"
	if _, err := config.ResourceCacheScope("unknown", "window"); err == nil {
		t.Fatal("invalid wildcard scope silently accepted")
	}
	if got, err := config.ResourceCacheScope("workspace", "navigation"); err != nil || got != ResourceCacheUser {
		t.Fatal("exact kind override lost")
	}
	var absent *MCPClient
	if got, err := absent.ResourceCacheScope("unknown", "window"); err != nil || got != ResourceCacheUser {
		t.Fatal("missing config no longer private")
	}
}
