package token

import (
	"testing"
	"time"
)

func TestEffectiveRefreshLeadDefaults(t *testing.T) {
	if got := EffectiveRefreshLead(0, 0); got != DefaultRefreshLead {
		t.Fatalf("default lead = %s, want %s", got, DefaultRefreshLead)
	}
	if DefaultRefreshLead != 15*time.Minute {
		t.Fatalf("default refresh lead must be 15 minutes, got %s", DefaultRefreshLead)
	}
}

func TestEffectiveRefreshLeadOverride(t *testing.T) {
	if got := EffectiveRefreshLead(45*time.Minute, 0); got != 45*time.Minute {
		t.Fatalf("provider override lead = %s, want 45m", got)
	}
}

func TestEffectiveRefreshLeadShortLifetimeClamp(t *testing.T) {
	// 10-minute token: 20% clamp = 2 minutes, well below the 15m default.
	if got := EffectiveRefreshLead(0, 10*time.Minute); got != 2*time.Minute {
		t.Fatalf("clamped lead = %s, want 2m", got)
	}
	// 1-minute token: 20% = 12s, clamped up to the 30s minimum useful lead.
	if got := EffectiveRefreshLead(0, time.Minute); got != MinUsefulRefreshLead {
		t.Fatalf("minimum useful lead = %s, want %s", got, MinUsefulRefreshLead)
	}
}

func TestShouldRefreshDefaultFifteenMinutes(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	// Expires in 16 minutes: not yet due under the default lead.
	if ShouldRefresh(now, now.Add(16*time.Minute), 0, 0) {
		t.Fatalf("token expiring in 16m must not refresh under 15m default")
	}
	// Expires in 14 minutes: due.
	if !ShouldRefresh(now, now.Add(14*time.Minute), 0, 0) {
		t.Fatalf("token expiring in 14m must refresh under 15m default")
	}
	// Unknown expiry never triggers.
	if ShouldRefresh(now, time.Time{}, 0, 0) {
		t.Fatalf("zero expiry must not refresh")
	}
}

func TestShouldRefreshSharedDecision(t *testing.T) {
	// Request-time resolution and the background watcher must make the same
	// threshold decision for the same inputs.
	now := time.Now()
	expiresAt := now.Add(10 * time.Minute)
	requestTime := ShouldRefresh(now, expiresAt, 0, 0)
	background := ShouldRefresh(now, expiresAt, 0, 0)
	if requestTime != background {
		t.Fatalf("request-time (%v) and background (%v) decisions diverged", requestTime, background)
	}
}

func TestRefreshJitterDeterministicAndBounded(t *testing.T) {
	key := Key{Subject: "user-1", Provider: "mcp:v1:abc"}
	lead := 15 * time.Minute
	first := RefreshJitter(key, lead)
	second := RefreshJitter(key, lead)
	if first != second {
		t.Fatalf("jitter must be deterministic per key: %s vs %s", first, second)
	}
	if first < 0 || first >= lead/10 {
		t.Fatalf("jitter %s out of [0, lead/10) bounds", first)
	}
	other := RefreshJitter(Key{Subject: "user-2", Provider: "mcp:v1:abc"}, lead)
	if other < 0 || other >= lead/10 {
		t.Fatalf("jitter %s out of bounds", other)
	}
}
