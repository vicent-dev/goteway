package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"goteway/pkg/repo"
)

// fakeStore is an in memory Store used by the service tests. It deliberately
// mirrors the real semantics that matter to the domain: not found lookups
// report repo.ErrNotFound, consumed registration tokens cannot be consumed
// twice, and WithinTx commits or discards every write.
//
// It does not assign identifiers the way the old auto increment columns did:
// those now come from the service, exactly as they come from a real adapter, so
// a test that reaches a store directly has to say which id it means.
type fakeStore struct {
	mu sync.Mutex

	users        map[ID]*User
	refreshToken map[string]*RefreshToken
	// Keyed by hash, because that is how a presented registration token is
	// looked up, and because a test holding only the raw value can find the
	// record it minted without knowing its id.
	regToken map[string]*RegistrationToken

	// hooks let a test fail a specific call, to exercise rollback and error
	// propagation.
	failCreateUser      error
	failCreateRefresh   error
	failCreateRegToken  error
	failConsumeRegToken error
	failRevokeAllByUser error
	failByTokenHash     error

	// committed reports whether the last WithinTx was committed.
	committed bool
	// txDepth counts nested WithinTx calls, which must not nest.
	txDepth int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:        make(map[ID]*User),
		refreshToken: make(map[string]*RefreshToken),
		regToken:     make(map[string]*RegistrationToken),
	}
}

func (s *fakeStore) CreateUser(_ context.Context, u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failCreateUser != nil {
		return s.failCreateUser
	}

	if u.ID == "" {
		return errors.New("fakeStore: CreateUser called without an id")
	}
	clone := *u
	s.users[u.ID] = &clone
	return nil
}

// UpdateUser replaces a stored account, so tests can disable one.
func (s *fakeStore) UpdateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	clone := *u
	s.users[u.ID] = &clone
	return nil
}

func (s *fakeStore) ByEmail(_ context.Context, email string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.users {
		if u.Email == email {
			clone := *u
			return &clone, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (s *fakeStore) ByID(_ context.Context, id ID) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	clone := *u
	return &clone, nil
}

func (s *fakeStore) CreateRefreshToken(_ context.Context, rt *RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failCreateRefresh != nil {
		return s.failCreateRefresh
	}

	if rt.ID == "" {
		return errors.New("fakeStore: CreateRefreshToken called without an id")
	}
	clone := *rt
	s.refreshToken[rt.JTI] = &clone
	return nil
}

func (s *fakeStore) ByJTI(_ context.Context, jti string) (*RefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.refreshToken[jti]
	if !ok {
		return nil, repo.ErrNotFound
	}
	clone := *rt
	return &clone, nil
}

func (s *fakeStore) UpdateRefreshToken(_ context.Context, rt *RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	clone := *rt
	s.refreshToken[rt.JTI] = &clone
	return nil
}

func (s *fakeStore) RevokeAllByUser(_ context.Context, userID ID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failRevokeAllByUser != nil {
		return s.failRevokeAllByUser
	}
	for _, rt := range s.refreshToken {
		if rt.UserID == userID && rt.RevokedAt == nil {
			revoked := at
			rt.RevokedAt = &revoked
		}
	}
	return nil
}

func (s *fakeStore) CreateRegistrationToken(_ context.Context, t *RegistrationToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failCreateRegToken != nil {
		return s.failCreateRegToken
	}

	if t.ID == "" {
		return errors.New("fakeStore: CreateRegistrationToken called without an id")
	}
	clone := *t
	s.regToken[t.TokenHash] = &clone
	return nil
}

func (s *fakeStore) ByTokenHash(_ context.Context, hash string) (*RegistrationToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failByTokenHash != nil {
		return nil, s.failByTokenHash
	}
	t, ok := s.regToken[hash]
	if !ok {
		return nil, repo.ErrNotFound
	}
	clone := *t
	return &clone, nil
}

func (s *fakeStore) ConsumeRegistrationToken(_ context.Context, id ID, usedBy ID, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failConsumeRegToken != nil {
		return false, s.failConsumeRegToken
	}

	// The port identifies the token by id, so the hash keyed map is searched
	// for it rather than the other way round.
	for _, t := range s.regToken {
		if t.ID != id {
			continue
		}
		if t.IsUsed() {
			return false, nil
		}

		used := at
		userID := usedBy
		t.UsedAt = &used
		t.UsedByUserID = &userID
		return true, nil
	}
	return false, repo.ErrNotFound
}

func (s *fakeStore) WithinTx(ctx context.Context, fn func(Store) error) error {
	s.mu.Lock()
	if s.txDepth > 0 {
		s.mu.Unlock()
		panic("fakeStore: nested WithinTx")
	}
	s.txDepth++
	s.committed = false
	s.mu.Unlock()

	// Snapshot so a failed transaction can be rolled back.
	snapshot := s.snapshot()
	err := fn(s)
	if err != nil {
		s.restoreSnapshot(snapshot)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.txDepth--
	s.committed = err == nil

	return err
}

type storeSnapshot struct {
	users        map[ID]*User
	refreshToken map[string]*RefreshToken
	regToken     map[string]*RegistrationToken
}

func (s *fakeStore) snapshot() storeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := storeSnapshot{
		users:        make(map[ID]*User, len(s.users)),
		refreshToken: make(map[string]*RefreshToken, len(s.refreshToken)),
		regToken:     make(map[string]*RegistrationToken, len(s.regToken)),
	}
	for id, u := range s.users {
		clone := *u
		snap.users[id] = &clone
	}
	for jti, rt := range s.refreshToken {
		clone := *rt
		snap.refreshToken[jti] = &clone
	}
	for id, t := range s.regToken {
		clone := *t
		snap.regToken[id] = &clone
	}
	return snap
}

func (s *fakeStore) restoreSnapshot(snap storeSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users = snap.users
	s.refreshToken = snap.refreshToken
	s.regToken = snap.regToken
}

// registrationToken returns the stored registration token with the given hash,
// which is how a test that only holds the raw value names the record it minted.
func (s *fakeStore) registrationToken(hash string) *RegistrationToken {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.regToken[hash]
	if !ok {
		return nil
	}
	clone := *t
	return &clone
}

func (s *fakeStore) storedRefreshToken(jti string) *RefreshToken {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.refreshToken[jti]
	if !ok {
		return nil
	}
	clone := *rt
	return &clone
}

func (s *fakeStore) sessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.refreshToken)
}
