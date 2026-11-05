package nemo

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// step is an action of an up or a down on one installation.
type step struct {
	name string
	// after are the installations it waits for.
	after []string
}

// schedule runs each step once the steps it comes after are done, starting
// them in the order given, and at most limit at once when limit is above 0. After a step fails, it
// starts no other, and waits for those under way: stopping an action
// midway would leave its installation half done. The steps must have no
// cycle.
func schedule(steps []step, limit int, act func(step step) error) error {
	type ended struct {
		name string
		err  error
	}
	inSet := map[string]bool{}
	for _, current := range steps {
		inSet[current.name] = true
	}
	endings := make(chan ended)
	started, done := map[string]bool{}, map[string]bool{}
	ready := func(current step) bool {
		return !slices.ContainsFunc(current.after, func(name string) bool { return inSet[name] && !done[name] })
	}
	var errs []error
	running := 0
	for {
		for _, current := range steps {
			if len(errs) > 0 || limit > 0 && running >= limit || started[current.name] || !ready(current) {
				continue
			}
			started[current.name] = true
			running++
			go func() { endings <- ended{current.name, act(current)} }()
		}
		if running == 0 {
			break
		}
		end := <-endings
		running--
		done[end.name] = true
		if end.err != nil {
			errs = append(errs, end.err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	var stuck []string
	for _, current := range steps {
		if !started[current.name] {
			stuck = append(stuck, current.name)
		}
	}
	if len(stuck) > 0 {
		return fmt.Errorf("%s could not start: what they wait for never came", strings.Join(stuck, ", "))
	}
	return nil
}

// levelled sorts the steps by level, a step one level after the deepest of
// the steps it comes after, and those of a level by name: the order they
// can go in. The steps must have no cycle.
func levelled(steps []step) []step {
	level := map[string]int{}
	var depth func(current step) int
	byName := map[string]step{}
	for _, current := range steps {
		byName[current.name] = current
	}
	depth = func(current step) int {
		if known, ok := level[current.name]; ok {
			return known
		}
		deepest := 0
		for _, name := range current.after {
			if before, inSet := byName[name]; inSet {
				deepest = max(deepest, depth(before)+1)
			}
		}
		level[current.name] = deepest
		return deepest
	}
	sorted := slices.Clone(steps)
	slices.SortStableFunc(sorted, func(left, right step) int {
		return cmp.Or(cmp.Compare(depth(left), depth(right)), cmp.Compare(left.name, right.name))
	})
	return sorted
}
