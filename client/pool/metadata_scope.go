package manager

import "context"

// MetadataConnectionScope selects the shared user-scoped connection already
// used by resource discovery. Execution callers retain their conversation ID.
// Unknown/custom and browser transports keep the caller's proposed scope.
func (m *Manager) MetadataConnectionScope(ctx context.Context, proposed, server string) string {
	if m == nil || m.newClientFn != nil || m.IsLocalClient(server) {
		return proposed
	}
	options, err := m.Options(ctx, server)
	if err != nil || options == nil || options.ClientOptions == nil || options.IsBrowserExecution() {
		return proposed
	}
	switch options.Transport.Type {
	case "streamable", "sse", "http":
		return ""
	default:
		return proposed
	}
}
