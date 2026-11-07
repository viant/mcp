// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core; provider bindings are independent of agent execution.
package observe

import (
	"context"
	"errors"
	identity "github.com/viant/mcp/client/catalog"
	"strings"
	"sync"
	"testing"
)

func TestDisabledAndOutcomeClassification(t *testing.T) {
	ctx := context.Background()
	if Enabled(ctx) {
		t.Fatal("default enabled")
	}
	if allocations := testing.AllocsPerRun(1000, func() { span := Start(ctx, ProviderGet, Labels{Kind: "report"}); span.End(OK, CacheUnknown) }); allocations != 0 {
		t.Fatal("disabled allocation", allocations)
	}
	for err, want := range map[error]Outcome{nil: OK, context.Canceled: Canceled, context.DeadlineExceeded: Canceled, identity.ErrResourceDenied: Denied, errors.New("secret error never emitted"): Error} {
		if OutcomeFor(err) != want {
			t.Fatal(err, want)
		}
	}
}
func TestConcurrentObserverAndBoundedPayload(t *testing.T) {
	var mu sync.Mutex
	events := []Event{}
	ctx := WithObserver(context.Background(), ObserverFunc(func(event Event) { mu.Lock(); events = append(events, event); mu.Unlock() }))
	if !Enabled(ctx) {
		t.Fatal("observer not found")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				span := Start(ctx, ProviderGet, Labels{Kind: "report", Provider: strings.Repeat("p", 200), Namespace: "users"})
				span.End(OK, CacheHit)
			}
		}()
	}
	wg.Wait()
	if len(events) != 1000 {
		t.Fatal(len(events))
	}
	for _, event := range events {
		if event.Duration < 0 || len(event.Provider) > 128 || event.Namespace != "users" || event.Kind != "report" || event.Phase != ProviderGet || event.Outcome != OK || event.Cache != CacheHit {
			t.Fatal(event)
		}
	}
}
func BenchmarkSpan(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		ctx := context.Background()
		if enabled {
			name = "enabled"
			ctx = WithObserver(ctx, ObserverFunc(func(Event) {}))
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				span := Start(ctx, ProviderGet, Labels{Kind: "report", Provider: "owner", Namespace: "users"})
				span.End(OK, CacheUnknown)
			}
		})
	}
}
