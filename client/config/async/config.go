// SPDX-License-Identifier: Apache-2.0
// MCP async tool configuration extracted from Viant Agently Core.
package asyncconfig

import "strings"

// ExecutionMode is the barrier/ownership decision for an async op.
//
// Exactly two values:
//
//   - "wait" (default): start registers the op and a later status call
//     parks on the barrier; poller + narrator are engaged and TimeoutAt
//     is enforced by the poller tick loop.
//   - "detach": start returns; the op is fire-and-forget at the runtime
//     layer — no PollAsyncOperation goroutine is spawned and no barrier
//     is attached. `TimeoutAt` is still populated from `TimeoutMs` so
//     the activated-status loop in `tool_executor.
//     maybeExecuteActivatedStatusTool` can time-box its re-polls for a
//     changed snapshot. When nothing observes the op, it is reclaimed
//     by the Manager GC after the workspace-configured
//     `default.async.gc.maxAge` idle window.
//
// There was previously a third value, "fork", intended for
// child-conversation launches. It behaved identically to "wait" in this
// package and was never set by any production caller. The "launch a
// child and wait" vs. "wait inline" distinction lives at the skill /
// agents layer (see service/skill and protocol/skill), not on this enum.
// Keep the async layer minimal: the only question here is "does the
// status call park or not?"
type ExecutionMode string

const (
	ExecutionModeWait   ExecutionMode = "wait"
	ExecutionModeDetach ExecutionMode = "detach"
)

type Config struct {
	Run                        RunConfig     `json:"run" yaml:"run"`
	Status                     StatusConfig  `json:"status" yaml:"status"`
	Cancel                     *CancelConfig `json:"cancel,omitempty" yaml:"cancel,omitempty"`
	DefaultExecutionMode       string        `json:"defaultExecutionMode,omitempty" yaml:"defaultExecutionMode,omitempty"`
	TerminalCarrierBeforeModel bool          `json:"terminalCarrierBeforeModel,omitempty" yaml:"terminalCarrierBeforeModel,omitempty"`
	TimeoutMs                  int           `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	PollIntervalMs             int           `json:"pollIntervalMs,omitempty" yaml:"pollIntervalMs,omitempty"`
	IdleTimeoutMs              int           `json:"idleTimeoutMs,omitempty" yaml:"idleTimeoutMs,omitempty"`
	// Narration controls how wait-mode async progress is surfaced to the parent
	// turn. Supported values:
	//   - "none": silent
	//   - "keydata": no narrator LLM; surface the async update directly from
	//     selector-extracted payload data/message (used by llm/agents)
	//   - "template": deterministic template rendering
	//   - "llm": supervising narrator LLM summarizes each async update
	Narration         string `json:"narration,omitempty" yaml:"narration,omitempty"`
	NarrationTemplate string `json:"narrationTemplate,omitempty" yaml:"narrationTemplate,omitempty"`
}

type RunConfig struct {
	Tool              string                 `json:"tool" yaml:"tool"`
	OperationIDPath   string                 `json:"operationIdPath" yaml:"operationIdPath"`
	ExecutionModePath string                 `json:"executionModePath,omitempty" yaml:"executionModePath,omitempty"`
	IntentPath        string                 `json:"intentPath,omitempty" yaml:"intentPath,omitempty"`
	SummaryPaths      []string               `json:"summaryPaths,omitempty" yaml:"summaryPaths,omitempty"`
	ExtraArgs         map[string]interface{} `json:"extraArgs,omitempty" yaml:"extraArgs,omitempty"`
	Selector          *Selector              `json:"selector,omitempty" yaml:"selector,omitempty"`
}

type StatusConfig struct {
	Tool           string                 `json:"tool" yaml:"tool"`
	OperationIDArg string                 `json:"operationIdArg" yaml:"operationIdArg"`
	ReuseRunArgs   bool                   `json:"reuseRunArgs,omitempty" yaml:"reuseRunArgs,omitempty"`
	ExtraArgs      map[string]interface{} `json:"extraArgs,omitempty" yaml:"extraArgs,omitempty"`
	Selector       Selector               `json:"selector" yaml:"selector"`
}

type CancelConfig struct {
	Tool           string `json:"tool" yaml:"tool"`
	OperationIDArg string `json:"operationIdArg" yaml:"operationIdArg"`
}

type Selector struct {
	StatusPath       string   `json:"statusPath" yaml:"statusPath"`
	MessagePath      string   `json:"messagePath,omitempty" yaml:"messagePath,omitempty"`
	DataPath         string   `json:"dataPath,omitempty" yaml:"dataPath,omitempty"`
	ProgressPath     string   `json:"progressPath,omitempty" yaml:"progressPath,omitempty"`
	ErrorPath        string   `json:"errorPath,omitempty" yaml:"errorPath,omitempty"`
	TerminalStatuses []string `json:"terminalStatuses,omitempty" yaml:"terminalStatuses,omitempty"`
}

// NormalizeExecutionMode coerces a mode string to the canonical enum
// value. Unknown values (historical "fork" entries, typos, empty) fall
// back to `fallback`, which itself defaults to "wait" if empty or also
// invalid.
func NormalizeExecutionMode(value string, fallback string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(fallback))
	}
	switch ExecutionMode(mode) {
	case ExecutionModeWait, ExecutionModeDetach:
		return mode
	default:
		if strings.TrimSpace(fallback) != "" && !strings.EqualFold(strings.TrimSpace(fallback), value) {
			return NormalizeExecutionMode(fallback, string(ExecutionModeWait))
		}
		return string(ExecutionModeWait)
	}
}

// ExecutionModeWaits reports whether an op in this mode parks on the
// status barrier. Only "wait" waits; "detach" does not.
func ExecutionModeWaits(value string) bool {
	return ExecutionMode(NormalizeExecutionMode(value, string(ExecutionModeWait))) == ExecutionModeWait
}
