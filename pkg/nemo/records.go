package nemo

import (
	"context"
	"errors"
	"fmt"

	"github.com/sntns/nemo/pkg/runtime"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/storage/volume"
	"github.com/sntns/nemo/pkg/target"
)

// ManagedError is a write refused on a stack another tool manages.
type ManagedError struct {
	Stack string
	Owner string
}

func (managed *ManagedError) Error() string {
	return fmt.Sprintf("stack %s is managed by %s: change it there, or give --force to write it anyway, which %s undoes at its next run", managed.Stack, managed.Owner, managed.Owner)
}

// session is what the operations under way on a stack share: its manifest,
// when nemo has it, and the store of its records on its target, when they
// are there.
type session struct {
	manifest *stack.Stack
	store    *volume.Store
	users    int
}

// records is the store of the stack's records: its target's, while an
// operation has it open, else the home's.
func (nemo *Nemo) records(stack string) storage.Store {
	nemo.mutex.Lock()
	defer nemo.mutex.Unlock()
	if opened, ok := nemo.opened[stack]; ok && opened.store != nil {
		return opened.store
	}
	return nemo.store
}

// reach gives the target the TLS the operation's manifest gives its
// address: the one a record keeps was where the action ran, a file of
// another machine perhaps.
func (nemo *Nemo) reach(stack string, deployment target.Target) target.Target {
	nemo.mutex.Lock()
	defer nemo.mutex.Unlock()
	opened, ok := nemo.opened[stack]
	if !ok || opened.manifest == nil {
		return deployment
	}
	if given := opened.manifest.Target; given.Host == deployment.Host && given.TLS != nil {
		deployment.TLS = given.TLS
	}
	return deployment
}

// open starts an operation on the stack: with its manifest, given or the
// home's, and its records, opened when the manifest keeps them on its
// target; held for writing, the stack is the operation's alone. It returns
// what ends the operation. An operation within another shares its session.
func (nemo *Nemo) open(ctx context.Context, manifest *stack.Stack, name string, held bool) (func() error, error) {
	nothing := func() error { return nil }
	if name == "" {
		return nothing, nil
	}
	nemo.mutex.Lock()
	if opened, ok := nemo.opened[name]; ok {
		opened.users++
		nemo.mutex.Unlock()
		return func() error { return nemo.close(ctx, name) }, nil
	}
	nemo.mutex.Unlock()

	if manifest == nil && nemo.manifests != nil {
		loaded, err := nemo.manifests.Load(name)
		switch {
		case errors.Is(err, storage.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			manifest = &loaded
		}
	}
	if manifest == nil {
		return nothing, nil
	}
	opened := &session{manifest: manifest, users: 1}
	end := func() error { return nemo.close(ctx, name) }
	if manifest.Records == stack.RecordsTarget {
		api, err := runtime.Connect(manifest.Target)
		if err != nil {
			return nil, err
		}
		if opened.store, err = volume.Open(ctx, api, name, held); err != nil {
			return nil, errors.Join(err, api.Close())
		}
		if held {
			if err := nemo.claim(ctx, opened.store, name); err != nil {
				return nil, errors.Join(err, opened.store.Close(ctx), api.Close())
			}
		}
		end = func() error { return errors.Join(nemo.close(ctx, name), api.Close()) }
	}
	nemo.mutex.Lock()
	if nemo.opened == nil {
		nemo.opened = map[string]*session{}
	}
	nemo.opened[name] = opened
	nemo.mutex.Unlock()
	return end, nil
}

// claim refuses a stack another tool manages, unless forced, and records
// this nemo's tool as the stack's manager when it has one.
func (nemo *Nemo) claim(ctx context.Context, store *volume.Store, name string) error {
	owner := store.Owner()
	if owner != "" && owner != nemo.owner && !nemo.force {
		return &ManagedError{Stack: name, Owner: owner}
	}
	if nemo.owner != "" && owner != nemo.owner {
		return store.SetOwner(ctx, nemo.owner)
	}
	return nil
}

func (nemo *Nemo) close(ctx context.Context, name string) error {
	nemo.mutex.Lock()
	opened := nemo.opened[name]
	opened.users--
	if opened.users > 0 {
		nemo.mutex.Unlock()
		return nil
	}
	delete(nemo.opened, name)
	nemo.mutex.Unlock()
	if opened.store == nil {
		return nil
	}
	return opened.store.Close(ctx)
}

// release closes what open opened, and keeps its error when there is none.
func release(done func() error, err *error) {
	*err = errors.Join(*err, done())
}
