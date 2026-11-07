package manager

import "strings"

const maxConfigDiffLines = 200

type ConfigDiff struct {
 Pending bool `json:"pending"`
 Added []string `json:"added"`
 Removed []string `json:"removed"`
 AddedCount int `json:"addedCount"`
 RemovedCount int `json:"removedCount"`
 SecretOnly bool `json:"secretOnly"`
 CoreChanged bool `json:"coreChanged"`
 OrderChanged bool `json:"orderChanged"`
 Truncated bool `json:"truncated"`
}

func configLines(text string) []string {
 text = strings.ReplaceAll(text, "\r\n", "\n")
 lines := make([]string, 0)
 for _, line := range strings.Split(text, "\n") {
  trimmed := strings.TrimSpace(line)
  if trimmed == "" || strings.HasPrefix(trimmed, "#") { continue }
  lines = append(lines, line)
 }
 return lines
}

func compareConfigLines(applied, draft string, limit int) (added, removed []string, addedCount, removedCount int, truncated bool) {
 if limit < 0 { limit = 0 }
 oldLines, newLines := configLines(applied), configLines(draft)
 newCounts := make(map[string]int, len(newLines))
 for _, line := range newLines { newCounts[line]++ }
 removed = make([]string, 0)
 for _, line := range oldLines {
  if newCounts[line] > 0 { newCounts[line]--; continue }
  removedCount++
  if len(removed) < limit { removed = append(removed, line) }
 }
 oldCounts := make(map[string]int, len(oldLines))
 for _, line := range oldLines { oldCounts[line]++ }
 added = make([]string, 0)
 for _, line := range newLines {
  if oldCounts[line] > 0 { oldCounts[line]--; continue }
  addedCount++
  if len(added) < limit { added = append(added, line) }
 }
 truncated = addedCount > len(added) || removedCount > len(removed)
 return
}

func buildConfigDiff(p *process, status RuntimeStatus, draft Rendered, activeCore string) ConfigDiff {
 added, removed, ac, rc, truncated := compareConfigLines(p.appliedConfig, draft.Text, maxConfigDiffLines)
 coreChanged := p.coreID != activeCore
 return ConfigDiff{
  Pending: draft.Digest != status.Applied || coreChanged,
  Added: added, Removed: removed, AddedCount: ac, RemovedCount: rc,
  SecretOnly: p.appliedConfig == draft.Text && draft.Digest != status.Applied && !coreChanged,
  CoreChanged: coreChanged,
  OrderChanged: p.appliedConfig != draft.Text && ac == 0 && rc == 0,
  Truncated: truncated,
 }
}
