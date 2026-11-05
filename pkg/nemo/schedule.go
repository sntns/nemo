package nemo

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// parallelActions bounds the actions under way at once: each may load its
// images into the target's one daemon.
const parallelActions = 4

// step is an action of an up or a down on one installation.
type step struct {
	name string
	// after are the installations it waits for.
	after []string
	// needs are the objects it waits for another step to announce.
	needs []object
	// creates are the objects it announces, which others need.
	creates []object
}

// schedule runs each step once the steps it comes after are done and the
// objects it needs are announced, at most limit at once, starting them in
// the order given. A step's objects count as announced once it ends, if it
// did not say so before. After a step fails, it starts no other, and waits
// for those under way: stopping an action midway would leave its
// installation half done. The steps must have no cycle.
func schedule(steps []step, limit int, act func(step step, announce func(object)) error) error {
	type message struct {
		ended     string
		err       error
		announced *object
	}
	inSet := map[string]bool{}
	for _, current := range steps {
		inSet[current.name] = true
	}
	messages := make(chan message)
	started, done := map[string]bool{}, map[string]bool{}
	announced := map[object]bool{}
	ready := func(current step) bool {
		return !slices.ContainsFunc(current.after, func(name string) bool { return inSet[name] && !done[name] }) &&
			!slices.ContainsFunc(current.needs, func(needed object) bool { return !announced[needed] })
	}
	var errs []error
	running := 0
	for {
		for _, current := range steps {
			if len(errs) > 0 || running >= limit || started[current.name] || !ready(current) {
				continue
			}
			started[current.name] = true
			running++
			go func() {
				err := act(current, func(created object) { messages <- message{announced: &created} })
				messages <- message{ended: current.name, err: err}
			}()
		}
		if running == 0 {
			break
		}
		received := <-messages
		if received.announced != nil {
			announced[*received.announced] = true
			continue
		}
		running--
		done[received.ended] = true
		for _, current := range steps {
			if current.name == received.ended {
				for _, created := range current.creates {
					announced[created] = true
				}
			}
		}
		if received.err != nil {
			errs = append(errs, received.err)
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
