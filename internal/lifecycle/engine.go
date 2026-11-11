// Package lifecycle drives asynchronous state transitions
// (e.g. CREATING -> ACTIVE, DELETING -> gone) via timers.
package lifecycle

import (
	"sync/atomic"
	"time"

	"github.com/morks/stackit_mock/internal/store"
)

type Engine struct {
	st    *store.Store
	delay atomic.Int64 // nanoseconds
}

func New(st *store.Store, delay time.Duration) *Engine {
	e := &Engine{st: st}
	e.delay.Store(int64(delay))
	return e
}

func (e *Engine) Delay() time.Duration     { return time.Duration(e.delay.Load()) }
func (e *Engine) SetDelay(d time.Duration) { e.delay.Store(int64(d)) }

// Create stores r with the initial status and schedules the transition to final.
func (e *Engine) Create(r store.Resource, initial, final string) (store.Resource, error) {
	r.Status = initial
	if final != "" && final != initial {
		r.Next = final
	}
	out, err := e.st.Create(r)
	if err != nil {
		return out, err
	}
	if out.Next != "" {
		e.schedule(out)
	}
	return out, nil
}

// Transition sets an intermediate status now and the final one after the delay.
func (e *Engine) Transition(typ, project, id, intermediate, final string) (store.Resource, bool) {
	out, ok := e.st.Update(typ, project, id, func(r *store.Resource) {
		r.Status, r.Next, r.NextDelete = intermediate, final, false
	})
	if ok {
		e.schedule(out)
	}
	return out, ok
}

// Delete marks the resource as deleting (if status != "") and removes it after the delay.
// Idempotent: returns false if the resource does not exist.
func (e *Engine) Delete(typ, project, id, deletingStatus string) (store.Resource, bool) {
	out, ok := e.st.Update(typ, project, id, func(r *store.Resource) {
		if deletingStatus != "" {
			r.Status = deletingStatus
		}
		r.Next, r.NextDelete = "", true
	})
	if ok {
		e.schedule(out)
	}
	return out, ok
}

// Resume re-arms pending transitions after a snapshot load.
func (e *Engine) Resume() {
	for _, r := range e.st.List(store.Filter{}) {
		if r.Next != "" || r.NextDelete {
			e.schedule(r)
		}
	}
}

func (e *Engine) schedule(r store.Resource) {
	apply := func() {
		cur, ok := e.st.Get(r.Type, r.Project, r.ID)
		if !ok || (cur.Next == "" && !cur.NextDelete) {
			return
		}
		if cur.NextDelete {
			e.st.Delete(cur.Type, cur.Project, cur.ID)
			return
		}
		e.st.Update(cur.Type, cur.Project, cur.ID, func(x *store.Resource) {
			x.Status, x.Next = x.Next, ""
		})
	}
	d := e.Delay()
	if d <= 0 {
		apply()
		return
	}
	time.AfterFunc(d, apply)
}
