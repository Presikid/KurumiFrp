package manager

import (
	"slices"
	"strings"
)

const maxConfigDiffLines = 120

type ConfigDiff struct {
	InstanceID    string   `json:"instanceId"`
	InstanceName  string   `json:"instanceName"`
	Revision      uint64   `json:"revision"`
	Changed       bool     `json:"changed"`
	Added         []string `json:"added"`
	Removed       []string `json:"removed"`
	AddedCount    int      `json:"addedCount"`
	RemovedCount  int      `json:"removedCount"`
	SecretChanged bool     `json:"secretChanged"`
	CoreChanged   bool     `json:"coreChanged"`
	OrderChanged  bool     `json:"orderChanged"`
	Truncated     bool     `json:"truncated"`
}

func diffLines(before, after string) (removed, added []string, removedCount, addedCount int, truncated bool) {
	oldLines := configDiffLines(before)
	newLines := configDiffLines(after)
	removed = []string{}
	added = []string{}
	counts := map[string]int{}
	for _, v := range newLines {
		counts[v]++
	}
	for _, v := range oldLines {
		if counts[v] > 0 {
			counts[v]--
			continue
		}
		removedCount++
		if len(removed) < maxConfigDiffLines {
			removed = append(removed, v)
		}
	}
	counts = map[string]int{}
	for _, v := range oldLines {
		counts[v]++
	}
	for _, v := range newLines {
		if counts[v] > 0 {
			counts[v]--
			continue
		}
		addedCount++
		if len(added) < maxConfigDiffLines {
			added = append(added, v)
		}
	}
	truncated = removedCount > len(removed) || addedCount > len(added)
	return
}
func configDiffLines(text string) []string {
	result := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && (!strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "# KurumiFRP control-entry policy (embedded only): ")) {
			result = append(result, line)
		}
	}
	return result
}
func compareConfig(id, name string, revision uint64, applied, draft string, secretChanged, coreChanged bool) ConfigDiff {
	r, a, rc, ac, tr := diffLines(applied, draft)
	orderChanged := rc == 0 && ac == 0 && !slices.Equal(configDiffLines(applied), configDiffLines(draft))
	return ConfigDiff{InstanceID: id, InstanceName: name, Revision: revision, Changed: rc > 0 || ac > 0 || orderChanged || secretChanged || coreChanged, Added: a, Removed: r, AddedCount: ac, RemovedCount: rc, SecretChanged: secretChanged, CoreChanged: coreChanged, OrderChanged: orderChanged, Truncated: tr}
}
