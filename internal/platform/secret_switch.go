package platform

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorequest/internal/core"
)

// ConflictPolicy decides what happens to a name that is bound in both the
// source and the target with different values. It is always chosen explicitly
// by the user; the zero value keeps the target value and never overwrites it.
type ConflictPolicy int

const (
	// ConflictKeepTarget leaves an existing, different target value in place.
	ConflictKeepTarget ConflictPolicy = iota
	// ConflictOverwriteTarget replaces an existing, different target value with
	// the source value.
	ConflictOverwriteTarget
)

// SecretSwitchEntry is one name considered by a switch. It never carries a
// value, only binding state and whether the two stores disagree.
type SecretSwitchEntry struct {
	Name        string
	SourceBound bool
	TargetBound bool
	Conflict    bool
}

// SecretSwitchPlan is the reviewable plan shown before any write happens. It
// records where each store really is, which names are covered, and whether the
// source could be enumerated at all.
type SecretSwitchPlan struct {
	Scope            string
	Source           SecretBackendInfo
	Target           SecretBackendInfo
	Entries          []SecretSwitchEntry
	SourceEnumerated bool
	TargetEnumerated bool
	Notes            []string
}

// HasConflicts reports whether any name differs between the two stores.
func (p SecretSwitchPlan) HasConflicts() bool {
	for _, e := range p.Entries {
		if e.Conflict {
			return true
		}
	}
	return false
}

// ConflictCount returns the number of differing names.
func (p SecretSwitchPlan) ConflictCount() int {
	n := 0
	for _, e := range p.Entries {
		if e.Conflict {
			n++
		}
	}
	return n
}

// CopyCount returns the number of names the source can supply.
func (p SecretSwitchPlan) CopyCount() int {
	n := 0
	for _, e := range p.Entries {
		if e.SourceBound {
			n++
		}
	}
	return n
}

// SecretSwitchResult reports what a successful switch did and keeps the undo
// journal needed if a later step (persisting the choice or refreshing the live
// providers) fails.
type SecretSwitchResult struct {
	Copied   []string
	Kept     []string
	Verified int

	undo func() error
}

// Rollback restores the target to its exact pre-switch values. It is a no-op
// after a switch that wrote nothing.
func (r SecretSwitchResult) Rollback() error {
	if r.undo == nil {
		return nil
	}
	return r.undo()
}

// BuildSecretSwitchPlan pre-reads both stores and produces the plan the user
// reviews. declared names come from the project's secret declarations; managed
// names come from the persistent manifest (the only way to recover entries the
// OS keychain cannot enumerate).
func BuildSecretSwitchPlan(scope string, source, target SecretBackend, declared, managed []string) (SecretSwitchPlan, error) {
	if source == nil || target == nil {
		return SecretSwitchPlan{}, &core.ConfigError{Err: errors.New("secret backend unavailable")}
	}
	plan := SecretSwitchPlan{
		Scope:  scope,
		Source: source.Info(),
		Target: target.Info(),
	}
	if prober, ok := source.(SecretBackendProber); ok {
		plan.Source = prober.Probe()
	}
	if prober, ok := target.(SecretBackendProber); ok {
		plan.Target = prober.Probe()
	}

	names := map[string]bool{}
	for _, n := range declared {
		if trimmed := strings.TrimSpace(n); trimmed != "" {
			names[trimmed] = true
		}
	}
	for _, n := range managed {
		if trimmed := strings.TrimSpace(n); trimmed != "" {
			names[trimmed] = true
		}
	}
	if listed, ok := source.Names(); ok {
		plan.SourceEnumerated = true
		for _, n := range listed {
			names[n] = true
		}
	}
	if listed, ok := target.Names(); ok {
		plan.TargetEnumerated = true
		for _, n := range listed {
			names[n] = true
		}
	}

	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		var srcVal, tgtVal string
		var srcOK, tgtOK bool
		var err error
		if plan.Source.Available {
			srcVal, srcOK, err = readSwitchSecret(source, name)
			if err != nil {
				return SecretSwitchPlan{}, err
			}
		}
		if plan.Target.Available {
			tgtVal, tgtOK, err = readSwitchSecret(target, name)
			if err != nil {
				return SecretSwitchPlan{}, err
			}
		}
		entry := SecretSwitchEntry{Name: name, SourceBound: srcOK, TargetBound: tgtOK}
		if srcOK && tgtOK && srcVal != tgtVal {
			entry.Conflict = true
		}
		plan.Entries = append(plan.Entries, entry)
	}

	if !plan.SourceEnumerated {
		plan.Notes = append(plan.Notes,
			"source backend cannot enumerate entries; only declared and managed names are covered, not every item in the source store")
	}
	if !plan.Source.Available {
		plan.Notes = append(plan.Notes, "source backend unavailable: "+plan.Source.Detail)
	}
	if !plan.Target.Available {
		plan.Notes = append(plan.Notes, "target backend unavailable: "+plan.Target.Detail)
	}
	if plan.HasConflicts() {
		plan.Notes = append(plan.Notes,
			fmt.Sprintf("%d name(s) have different values in both stores; choose keep-target or overwrite-target explicitly", plan.ConflictCount()))
	}
	return plan, nil
}

type switchUndo struct {
	name  string
	had   bool
	value string
}

// ExecuteSecretSwitch copies every source value into the target, verifies each
// write by reading it back, and returns an undo journal. On any failure it
// rolls the target back to its pre-switch state and leaves the source
// untouched. The selection must be persisted by the caller only after this
// returns nil.
func ExecuteSecretSwitch(source, target SecretBackend, plan SecretSwitchPlan, policy ConflictPolicy) (SecretSwitchResult, error) {
	var result SecretSwitchResult
	if source == nil || target == nil {
		return result, &core.ConfigError{Err: errors.New("secret backend unavailable")}
	}
	if policy != ConflictKeepTarget && policy != ConflictOverwriteTarget {
		return result, &core.ConfigError{Err: errors.New("invalid secret conflict policy")}
	}
	if !plan.Source.Available {
		return result, &core.ConfigError{Err: fmt.Errorf("source secret store unavailable: %s", plan.Source.Detail)}
	}
	if !plan.Target.Available {
		return result, &core.ConfigError{Err: fmt.Errorf("target secret store unavailable: %s", plan.Target.Detail)}
	}

	// Availability may change while the user reviews the plan, even when
	// there are no named entries to inspect.
	for _, backend := range []SecretBackend{source, target} {
		if prober, ok := backend.(SecretBackendProber); ok {
			if info := prober.Probe(); !info.Available {
				return result, &core.ConfigError{Err: fmt.Errorf("%s secret store is no longer available", info.Kind)}
			}
		}
	}

	var undos []switchUndo
	rollback := func(cause error) error {
		if rbErr := rollbackSwitchWrites(target, undos); rbErr != nil {
			return fmt.Errorf("%w; %v", cause, rbErr)
		}
		return cause
	}

	for _, entry := range plan.Entries {
		name := entry.Name
		srcVal, srcOK, err := readSwitchSecret(source, name)
		if err != nil {
			return result, rollback(err)
		}
		if !srcOK {
			continue // source no longer has this value; it is not a copy source
		}
		tgtVal, tgtOK, err := readSwitchSecret(target, name)
		if err != nil {
			return result, rollback(err)
		}
		if tgtOK && tgtVal == srcVal {
			result.Verified++
			continue
		}
		if tgtOK && tgtVal != srcVal && policy == ConflictKeepTarget {
			result.Kept = append(result.Kept, name)
			continue
		}

		// Journal before Set: a backend may persist and report a late error.
		undos = append(undos, switchUndo{name: name, had: tgtOK, value: tgtVal})
		if err := target.Set(name, srcVal); err != nil {
			return result, rollback(&core.ConfigError{Err: fmt.Errorf("copying secret %q to the target store: %w", name, err)})
		}

		got, ok := target.Get(name)
		if !ok || got != srcVal {
			return result, rollback(&core.ConfigError{Err: fmt.Errorf("read-back verification failed for secret %q", name)})
		}
		result.Copied = append(result.Copied, name)
		result.Verified++
	}

	if len(undos) > 0 {
		result.undo = func() error { return rollbackSwitchWrites(target, undos) }
	}
	return result, nil
}

func rollbackSwitchWrites(target SecretBackend, undos []switchUndo) error {
	var failures []string
	for i := len(undos) - 1; i >= 0; i-- {
		u := undos[i]
		var err error
		if u.had {
			err = target.Set(u.name, u.value)
		} else {
			err = target.Delete(u.name)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", u.name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("target rollback incomplete: %s", strings.Join(failures, "; "))
	}
	return nil
}

// A false Get alone cannot distinguish absence from an unreadable store.
func readSwitchSecret(backend SecretBackend, name string) (string, bool, error) {
	status := backend.InspectSecret(name)
	switch status.State {
	case core.SecretStateMissing:
		return "", false, nil
	case core.SecretStateBound:
		if value, ok := backend.Get(name); ok {
			return value, true, nil
		}
	}
	return "", false, &core.ConfigError{Err: fmt.Errorf("cannot read secret %q from the %s store; switching was stopped", name, backend.Info().Kind)}
}
