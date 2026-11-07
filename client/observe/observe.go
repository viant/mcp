// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core; provider bindings are independent of agent execution.
// Package observe provides optional timing at shared primitive boundaries.
// Events contain trusted catalog labels only, never request payloads, URIs,
// identity, credentials or raw errors. Nested spans overlap: durations are not
// additive. A provider/dispatch total includes its child spans and source waits.
package observe

import (
	"context"
	"errors"
	"time"

	identity "github.com/viant/mcp/client/catalog"
)

type Phase string

const (
	GatewayTool           Phase = "gateway.tool"
	GatewayRequestEncode  Phase = "gateway.request.encode"
	GatewayMCPCall        Phase = "gateway.mcp.call"
	GatewayResponseDecode Phase = "gateway.response.decode"
	ProviderNamespaceList Phase = "provider.namespace.list"
	ProviderNamespaceGet  Phase = "provider.namespace.get"
	ProviderList          Phase = "provider.list"
	ProviderGet           Phase = "provider.get"
	ProviderWrite         Phase = "provider.write"
	SourceInventory       Phase = "source.inventory"
	SourceLookup          Phase = "source.lookup"
	SourceWrite           Phase = "source.write"
	SourceMetadata        Phase = "source.metadata"
	BindingResolver       Phase = "provider.binding.resolver"
	RevisionResolve       Phase = "provider.revision.resolve"
	RevisionRead          Phase = "provider.revision.read"
	Validation            Phase = "provider.validation"
	Authorization         Phase = "provider.authorization"
	Identity              Phase = "provider.identity"
	MenuVisibility        Phase = "provider.menu.visibility"
	MCPDispatch           Phase = "mcp.dispatch"
	Serialization         Phase = "mcp.serialization"
)

type Outcome string

const (
	OK       Outcome = "ok"
	Error    Outcome = "error"
	Denied   Outcome = "denied"
	Canceled Outcome = "canceled"
)

type Cache string

const (
	CacheUnknown Cache = "unknown"
	CacheHit     Cache = "hit"
	CacheMiss    Cache = "miss"
	CacheBypass  Cache = "bypass"
)

// Labels must be trusted, configured metadata; Namespace is never a resource
// name/user path or a raw caller filter. Labels are length-bounded on emission.
type Labels struct{ Kind, Provider, Namespace string }
type Event struct {
	Phase Phase
	Labels
	Duration time.Duration
	Outcome  Outcome
	Cache    Cache
	Bytes    int64 // optional transferred or decoded byte count; never content
}

// Observer callbacks execute synchronously, must be safe for concurrent calls,
// and should not block or panic. Context/request data is intentionally absent.
type Observer interface{ Observe(Event) }
type ObserverFunc func(Event)

func (f ObserverFunc) Observe(event Event) { f(event) }

type key struct{}

func WithObserver(ctx context.Context, observer Observer) context.Context {
	return context.WithValue(ctx, key{}, observer)
}
func Enabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	observer, _ := ctx.Value(key{}).(Observer)
	return observer != nil
}

// Span is a value, not a deferred closure. The zero/disabled value is a no-op.
type Span struct {
	observer Observer
	start    time.Time
	phase    Phase
	labels   Labels
}

func Start(ctx context.Context, phase Phase, labels Labels) Span {
	if ctx == nil {
		return Span{}
	}
	observer, _ := ctx.Value(key{}).(Observer)
	if observer == nil {
		return Span{}
	}
	return Span{observer: observer, start: time.Now(), phase: phase, labels: labels}
}
func bounded(value string) string {
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
func (span Span) End(outcome Outcome, cache Cache) {
	span.EndBytes(outcome, cache, 0)
}
func (span Span) EndBytes(outcome Outcome, cache Cache, bytes int64) {
	if span.observer == nil {
		return
	}
	duration := time.Since(span.start)
	span.observer.Observe(Event{Phase: span.phase, Labels: Labels{Kind: bounded(span.labels.Kind), Provider: bounded(span.labels.Provider), Namespace: bounded(span.labels.Namespace)}, Duration: duration, Outcome: outcome, Cache: cache, Bytes: bytes})
}
func OutcomeFor(err error) Outcome {
	if err == nil {
		return OK
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Canceled
	}
	if errors.Is(err, identity.ErrResourceDenied) {
		return Denied
	}
	return Error
}
