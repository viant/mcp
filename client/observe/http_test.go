// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core; provider bindings are independent of agent execution.
package observe

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPPreservesWriterAndCollectsDetachedMetadataOnly(t *testing.T) {
	w := httptest.NewRecorder()
	var values map[string]float64
	next := http.HandlerFunc(func(actual http.ResponseWriter, r *http.Request) {
		if actual != w {
			t.Fatal("writer identity changed")
		}
		if !Enabled(r.Context()) {
			t.Fatal("observer missing")
		}
		span := Start(r.Context(), ProviderGet, Labels{Namespace: "private-user-path"})
		actual.(http.Flusher).Flush()
		span.End(OK, CacheHit)
	})
	if HTTP(next, nil) == nil {
		t.Fatal("missing disabled handler")
	}
	HTTP(next, func(v map[string]float64) { values = v }).ServeHTTP(w, httptest.NewRequest("GET", "/secret?token=private", nil))
	if values["provider.get.calls"] != 1 || values["http.total.calls"] != 1 || values["provider.get.cache.hit"] != 1 || len(values) != 5 {
		t.Fatalf("unexpected totals: %v", values)
	}
	if !w.Flushed {
		t.Fatal("flush lost")
	}
}

func TestCollectorKeepsCacheOutcomesWithoutUnboundedLabels(t *testing.T) {
	collector := &Collector{}
	for _, cache := range []Cache{CacheHit, CacheMiss, CacheBypass, CacheUnknown, Cache("untrusted-value")} {
		collector.Observe(Event{Phase: GatewayDefinitionDecode, Outcome: OK, Cache: cache, Labels: Labels{Namespace: "private-user-path"}})
	}
	values := collector.Totals()
	if values["gateway.definition.decode.calls"] != 5 || values["gateway.definition.decode.cache.hit"] != 1 || values["gateway.definition.decode.cache.miss"] != 1 || values["gateway.definition.decode.cache.bypass"] != 1 || len(values) != 5 {
		t.Fatalf("unexpected bounded cache totals: %v", values)
	}
}

func TestCollectorConcurrentTotalsAreDetached(t *testing.T) {
	collector := &Collector{}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 100; n++ {
				collector.Observe(Event{Phase: ProviderGet, Duration: time.Millisecond, Outcome: OK})
			}
		}()
	}
	workers.Wait()
	values := collector.Totals()
	if values["provider.get.calls"] != 800 || values["provider.get.ms"] != 800 {
		t.Fatal(values)
	}
	values["provider.get.calls"] = 0
	if collector.Totals()["provider.get.calls"] != 800 {
		t.Fatal("caller mutated collector")
	}
}
