package core

import (
	"runtime"
	"sync"
	"weak"
)

// PlanConfirmGate returns the exact-scope gate attached by SetPlanConfirmGate.
// It is stored in a small weak-keyed table so ExecuteOptions needs no public
// field change in this worktree, and no global strong UI state is retained.
func PlanConfirmGate(plan *Plan) *OneShotConfirmGate {
	if plan == nil {
		return nil
	}
	gateMu.RLock()
	defer gateMu.RUnlock()
	return gateByPlan[weak.Make(plan)]
}

var (
	gateMu     sync.RWMutex
	gateByPlan = map[weak.Pointer[Plan]]*OneShotConfirmGate{}
)

// setPlanConfirmGate records gate for this exact Plan and returns it on
// success. A redirect cannot consume it: only Hop 0 asks for authorization and
// the gate allows exactly one exact-scope match.
func setPlanConfirmGate(plan *Plan, gate *OneShotConfirmGate) *OneShotConfirmGate {
	if plan == nil || gate == nil {
		return nil
	}
	key := weak.Make(plan)
	gateMu.Lock()
	gateByPlan[key] = gate
	gateMu.Unlock()
	runtime.AddCleanup(plan, func(k weak.Pointer[Plan]) {
		gateMu.Lock()
		delete(gateByPlan, k)
		gateMu.Unlock()
	}, key)
	return gate
}
