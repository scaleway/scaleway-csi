// Vendored from oya.to/namedlocker v1.0.0 (MIT License, Copyright (c) 2020 oyato cloud)
//
// Package namedlocker implements in-memory named locks.
package namedlocker

import (
	"errors"
	"sync"
)

var (
	// ErrUnlockOfUnlockedKey is the error reported when unlocking an unlocked key.
	ErrUnlockOfUnlockedKey = errors.New("unlock of unlocked key")

	pool = sync.Pool{New: func() any { return &ref{} }}
)

type ref struct {
	sync.Mutex
	cnt int32
}

// Store is an in-memory store of named locks.
//
// The zero-value is usable as-is.
type Store struct {
	mu   sync.Mutex
	refs map[string]*ref
}

// Lock acquires a lock on key.
// If key is locked, it blocks until it can be acquired.
func (s *Store) Lock(key string) {
	s.mu.Lock()
	r, ok := s.refs[key]
	if !ok {
		r = pool.Get().(*ref)
		if s.refs == nil {
			s.refs = map[string]*ref{}
		}
		s.refs[key] = r
	}
	r.cnt++
	s.mu.Unlock()

	r.Lock()
}

// TryUnlock releases the lock on key.
//
// If key is not locked, ErrUnlockOfUnlockedKey is returned.
func (s *Store) TryUnlock(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.refs[key]
	if !ok || r.cnt <= 0 {
		// should we panic if cnt is < 0? that suggests some state got corrupted.
		return ErrUnlockOfUnlockedKey
	}
	r.Unlock()
	r.cnt--
	if r.cnt == 0 {
		delete(s.refs, key)
		pool.Put(r)
	}
	return nil
}

// Unlock is a wrapper around TryUnlock that panics if it returns an error.
func (s *Store) Unlock(key string) {
	err := s.TryUnlock(key)
	if err != nil {
		panic(err)
	}
}
