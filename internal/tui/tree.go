// Package tui contains the Bubble Tea model for the mock inspector.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/morks/stackit_mock/internal/store"
)

// Row is one visible line of the tree.
type Row struct {
	Depth    int
	Label    string
	Resource *store.Resource // nil for project/service header rows
}

// BuildRows groups resources as Project -> Service -> Resource, applying a
// case-insensitive substring filter over type, name, id and status.
func BuildRows(res []store.Resource, filter string) []Row {
	filter = strings.ToLower(strings.TrimSpace(filter))
	byProject := map[string]map[string][]store.Resource{}
	for _, r := range res {
		if filter != "" && !strings.Contains(strings.ToLower(r.Type+" "+r.Name+" "+r.ID+" "+r.Status), filter) {
			continue
		}
		p := r.Project
		if p == "" {
			p = "(global)"
		}
		if byProject[p] == nil {
			byProject[p] = map[string][]store.Resource{}
		}
		byProject[p][r.Service] = append(byProject[p][r.Service], r)
	}
	var rows []Row
	for _, p := range sortedKeys(byProject) {
		rows = append(rows, Row{Depth: 0, Label: "▾ " + p})
		for _, s := range sortedKeys(byProject[p]) {
			items := byProject[p][s]
			rows = append(rows, Row{Depth: 1, Label: fmt.Sprintf("▾ %s (%d)", s, len(items))})
			for i := range items {
				r := items[i]
				name := r.Name
				if name == "" {
					name = r.ID
				}
				kind := r.Type[strings.LastIndex(r.Type, "/")+1:]
				rows = append(rows, Row{Depth: 2, Label: fmt.Sprintf("%s  %s  [%s]", kind, name, r.Status), Resource: &r})
			}
		}
	}
	return rows
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}
