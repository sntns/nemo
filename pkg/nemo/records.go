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

// session is the store of a stack's records on its target, open for the
// operations under way.
type session struct {
	store *volume.Store
	users int
}

// records is the store of the stack's records: its target's, while an
// operation has it open, else the home's.
func (nemo *Nemo) records(stack string) storage.Store {
	nemo.mutex.Lock()
	defer nemo.mutex.Unlock()
	if opened, ok := nemo.opened[stack]; ok {
		return opened.store
	}
	return nemo.store
}

// open opens the records of the stack for an operation, when its manifest,
// given or the home's, keeps them on its target; held for writing, the
// stack is the operation's alone. It returns what closes them. An operation
// within another shares its records.
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
	if manifest == nil || manifest.Records != stack.RecordsTarget {
		return nothing, nil
	}

	api, err := runtime.Connect(nemo.reach(manifest.Target))
	if err != nil {
		return nil, err
	}
	store, err := volume.Open(ctx, api, name, held)
	if err != nil {
		return nil, errors.Join(err, api.Close())
	}
	if held {
		if err := nemo.claim(ctx, store, name); err != nil {
			return nil, errors.Join(err, store.Close(ctx), api.Close())
		}
	}
	nemo.mutex.Lock()
	if nemo.opened == nil {
		nemo.opened = map[string]*session{}
	}
	nemo.opened[name] = &session{store: store, users: 1}
	nemo.mutex.Unlock()
	return func() error { return errors.Join(nemo.close(ctx, name), api.Close()) }, nil
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
	return opened.store.Close(ctx)
}

// reach gives the target the TLS nemo holds for its address.
func (nemo *Nemo) reach(deployment target.Target) target.Target {
	if tls, ok := nemo.tls[deployment.Host]; ok {
		deployment.TLS = &tls
	}
	return deployment
}

// release closes what open opened, and keeps its error when there is none.
func release(done func() error, err *error) {
	*err = errors.Join(*err, done())
}
