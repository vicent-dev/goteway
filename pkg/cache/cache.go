// Package cache holds the generic response cache port and its Redis
// implementation. The key and the serialized value are produced by the
// Cacheable value being stored, not by the cache, so that a domain decides what
// its own fingerprint looks like.
package cache

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get when the key is simply absent. Adapters
// translate driver specific misses into this sentinel, so that a miss can be
// told apart from a cache that is unreachable without importing the driver.
var ErrNotFound = errors.New("cache: key not found")

// Cacheable is a value that knows how to name itself and how to turn itself
// into, and back from, the bytes the cache stores.
//
// Every method reports its own failure: a value that cannot be encoded or
// decoded is a different problem from a cache that cannot be reached, and the
// caller has to be able to tell them apart.
type Cacheable interface {
	Key() string
	Value() (string, error)
	SetValue(string) error
}

// Cache is the generic store contract. A cache that cannot be reached returns
// an error rather than pretending the key was absent: silently answering "no
// value" is indistinguishable from a cold cache, and hides the outage.
//
// Get returns ErrNotFound for a genuine miss, which callers are expected to
// handle as a normal outcome rather than as a failure.
type Cache[T Cacheable] interface {
	Set(ctx context.Context, value T) error
	Get(ctx context.Context, value T) error
}
