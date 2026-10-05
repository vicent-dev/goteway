package auth

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ID identifies a row owned by this package: a ULID, i.e. a 48 bit millisecond
// timestamp followed by 80 bits of entropy, rendered as 26 characters of
// Crockford base32.
//
// It replaces the auto incrementing integer this domain used to carry, which
// made the identifiers guessable and told any caller who received one how many
// rows had been written before it. A ULID gives three things back:
//
//   - They are not enumerable. Account ids are exposed on every register,
//     login and refresh response, so sequential ones handed out the size of the
//     user table to anyone who registered two accounts.
//   - They sort by creation time. varchar(26) ordering is chronological, so a
//     query ordered by id is ordered by age without an extra index, and the
//     rotation chains in refresh_tokens read in the order they happened.
//   - They are minted by the domain, not by the database. The service assigns
//     an id before it stores anything, so a record can never be written under
//     an empty or reused key.
//
// The timestamp half is not a secret: a ULID says when a row was created, which
// the created_at column already says. It is a public identifier, so ids that
// are credentials are a different thing entirely — the raw registration token
// stays 32 bytes of crypto/rand for exactly that reason (see NewOpaqueToken).
//
// It is stored as its 26 character string rather than as the library's [16]byte
// array: GORM reads an array as a blob, which is a different column type on
// every driver, and the text form is what the API and the JWT subject carry
// anyway.
type ID string

// String returns the identifier as it is stored and serialized.
func (i ID) String() string {
	return string(i)
}

// newID returns a fresh identifier stamped with t. The entropy is the
// library's process wide monotonic source, so two identifiers minted inside the
// same millisecond are still distinct and still increasing, which is what makes
// a frozen test clock safe.
func newID(t time.Time) (ID, error) {
	id, err := ulid.New(ulid.Timestamp(t), ulid.DefaultEntropy())
	if err != nil {
		return "", fmt.Errorf("auth: generate id at %v: %w", t, err)
	}
	return ID(id.String()), nil
}

// newJTI returns the identifier a single token is known by, both inside the
// token and in the stored session.
func newJTI(now time.Time) (string, error) {
	id, err := newID(now)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// parseID reads an identifier back out of its serialized form, reporting
// ErrInvalidToken for anything that is not one. That is the only caller of an
// identifier that cannot be trusted — the subject of a presented token — so it
// is the only place the strictness matters.
func parseID(raw string) (ID, error) {
	id, err := ulid.ParseStrict(raw)
	if err != nil {
		return "", ErrInvalidToken
	}
	// The zero ULID parses, so it needs refusing explicitly: it is what a
	// missing or zeroed subject turns into, and it identifies nothing.
	if id.IsZero() {
		return "", ErrInvalidToken
	}
	// Canonicalised rather than echoed, so a lowercased subject still finds the
	// row it names, since the stored form is always upper case.
	return ID(id.String()), nil
}
