package lifecycle

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/morks/stackit_mock/internal/store"
)

func TestCreateTransitionsAndDelete(t *testing.T) {
	st := store.New()
	e := New(st, 20*time.Millisecond)
	r := store.Resource{Type: "t", Service: "s", Project: "p", ID: "1", Name: "n"}
	if _, err := e.Create(r, "CREATING", "ACTIVE"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(r, "CREATING", "ACTIVE"); err != store.ErrExists {
		t.Fatalf("want ErrExists, got %v", err)
	}
	got, _ := st.Get("t", "p", "1")
	if got.Status != "CREATING" {
		t.Fatalf("status %s", got.Status)
	}
	time.Sleep(80 * time.Millisecond)
	got, _ = st.Get("t", "p", "1")
	if got.Status != "ACTIVE" {
		t.Fatalf("status %s", got.Status)
	}
	if _, ok := e.Delete("t", "p", "1", "DELETING"); !ok {
		t.Fatal("delete missing")
	}
	got, _ = st.Get("t", "p", "1")
	if got.Status != "DELETING" {
		t.Fatalf("status %s", got.Status)
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := st.Get("t", "p", "1"); ok {
		t.Fatal("still present")
	}
	if _, ok := e.Delete("t", "p", "1", ""); ok {
		t.Fatal("second delete should report missing")
	}
}

func TestSnapshotResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st := store.New()
	e := New(st, time.Hour)
	e.Create(store.Resource{Type: "t", Project: "p", ID: "1"}, "CREATING", "ACTIVE")
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	st2 := store.New()
	if err := st2.Load(path); err != nil {
		t.Fatal(err)
	}
	New(st2, 10*time.Millisecond).Resume()
	time.Sleep(60 * time.Millisecond)
	if r, _ := st2.Get("t", "p", "1"); r.Status != "ACTIVE" {
		t.Fatalf("status %s", r.Status)
	}
}
