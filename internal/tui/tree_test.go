package tui

import (
	"testing"

	"github.com/morks/stackit_mock/internal/store"
)

func TestBuildRows(t *testing.T) {
	res := []store.Resource{
		{Type: "iaas/network", Service: "iaas", Project: "p1", ID: "1", Name: "net", Status: "CREATED"},
		{Type: "ske/cluster", Service: "ske", Project: "p1", ID: "c", Name: "k8s", Status: "STATE_HEALTHY"},
		{Type: "resourcemanager/project", Service: "resourcemanager", ID: "p1", Name: "proj"},
	}
	tests := []struct {
		name, filter string
		want         int
	}{
		{"all", "", 3 + 1 + 3 + 2 - 1}, // global(1+1+1) + p1(1+2+2) = 8
		{"filter ske", "SKE", 3},
		{"no match", "zzz", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(BuildRows(res, tt.filter)); got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}
