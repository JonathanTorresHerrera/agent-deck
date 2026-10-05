package ui

import (
	"sort"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 39 (provider counts): the filter bar (header line 2) shows how many
// LIVE sessions each provider has, e.g. "claude 65 (7▶) · codex 2 · grok 1".
// Live = running + waiting + idle, in the archive partition the list renders
// (same rule as the patch-38 status counts). The ▶ figure is how many of
// them are running right now.

type providerCount struct {
	tool    string
	live    int
	running int
}

// providerCounts tallies live sessions per tool from the render snapshot,
// most-live first (ties by name). Sessions without a tool count as "shell".
func (h *Home) providerCounts() []providerCount {
	snapshot := h.getSessionRenderSnapshot()
	byTool := map[string]*providerCount{}
	for _, state := range snapshot {
		// v1.16.22: same rows the header tallies count (superseded cross-harness
		// sources skipped; starting/queued bucket as idle via statusBucket).
		if state.archivedSuperseded || !h.archivedInRenderedPartition(state.archived) {
			continue
		}
		switch statusBucket(state.status) {
		case session.StatusRunning, session.StatusWaiting, session.StatusIdle:
		default:
			continue
		}
		tool := strings.TrimSpace(state.tool)
		if tool == "" {
			tool = "shell"
		}
		pc := byTool[tool]
		if pc == nil {
			pc = &providerCount{tool: tool}
			byTool[tool] = pc
		}
		pc.live++
		if state.status == session.StatusRunning {
			pc.running++
		}
	}
	out := make([]providerCount, 0, len(byTool))
	for _, pc := range byTool {
		out = append(out, *pc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].live != out[j].live {
			return out[i].live > out[j].live
		}
		return out[i].tool < out[j].tool
	})
	return out
}

// renderProviderCounts renders the segment at a detail level:
// 0 = "C Claude 65 (7>) . G ChatGPT 2", 1 = without the running figure,
// 2 = nothing. Returns "" when there are no live sessions. Patch 45 added the
// badge letters and display names (renderProviderCountsSel).
func renderProviderCounts(counts []providerCount, level int) string {
	return renderProviderCountsSel(counts, level, "")
}
