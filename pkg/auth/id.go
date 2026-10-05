package auth

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

type ID string

func (i ID) String() string {
	return string(i)
}

func newID(t time.Time) (ID, error) {
	id, err := ulid.New(ulid.Timestamp(t), ulid.DefaultEntropy())
	if err != nil {
		return "", fmt.Errorf("auth: generate id at %v: %w", t, err)
	}
	return ID(id.String()), nil
}

func newJTI(now time.Time) (string, error) {
	id, err := newID(now)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func parseID(raw string) (ID, error) {
	id, err := ulid.ParseStrict(raw)
	if err != nil {
		return "", ErrInvalidToken
	}

	if id.IsZero() {
		return "", ErrInvalidToken
	}

	return ID(id.String()), nil
}
