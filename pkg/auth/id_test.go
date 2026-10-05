package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testID returns a known, valid identifier, so a test that needs a user id
// does not have to mint one first.
func testID() ID {
	return ID("01HQZX0000000000000000000A")
}

// otherID is a second valid identifier, for the tests that need two.
func otherID() ID {
	return ID("01HQZX0000000000000000000B")
}

func TestNewIDIsACanonicalULID(t *testing.T) {
	id, err := newID(time.Now())

	require.NoError(t, err)
	assert.Len(t, id, 26)
	// Crockford base32: no I, L, O or U, so it cannot be confused with 1 and 0.
	assert.Regexp(t, "^[0-9A-HJKMNP-TV-Z]{26}$", id.String())

	// It is a ULID and not merely 26 characters of anything.
	parsed, err := parseID(id.String())
	require.NoError(t, err)
	assert.Equal(t, id, parsed)
}

func TestNewIDStampsTheGivenClock(t *testing.T) {
	at := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	id, err := newID(at)

	require.NoError(t, err)
	// The first ten characters are the timestamp in base32, so two ids minted
	// a second apart differ there whatever the entropy does.
	later, err := newID(at.Add(time.Second))
	require.NoError(t, err)
	assert.NotEqual(t, id[:10], later[:10])
}

func TestNewIDSortsByCreationTime(t *testing.T) {
	at := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	older, err := newID(at)
	require.NoError(t, err)
	newer, err := newID(at.Add(time.Hour))
	require.NoError(t, err)

	// Plain string ordering, which is what a varchar(26) index gives in the
	// database too.
	assert.Less(t, older.String(), newer.String())
}

func TestNewIDIsUniqueAndIncreasingWithinAMillisecond(t *testing.T) {
	at := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	// A frozen clock is what the service tests run on, so this is the case that
	// would break if the entropy were not monotonic.
	first, err := newID(at)
	require.NoError(t, err)
	second, err := newID(at)
	require.NoError(t, err)
	third, err := newID(at)
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	assert.NotEqual(t, second, third)
	assert.Less(t, first.String(), second.String())
	assert.Less(t, second.String(), third.String())
}

func TestNewIDReportsAnUnrepresentableClockInsteadOfPanicking(t *testing.T) {
	// A ULID timestamp is 48 bits of milliseconds. A clock outside that range
	// cannot be encoded, and the library panics unless the error is handled.
	_, err := newID(time.Date(-1, time.January, 1, 0, 0, 0, 0, time.UTC))

	assert.Error(t, err)
}

func TestNewJTIIsAValidIdentifier(t *testing.T) {
	jti, err := newJTI(time.Now())

	require.NoError(t, err)
	assert.Len(t, jti, 26)
	_, err = parseID(jti)
	assert.NoError(t, err, "a jti must be readable as the identifier it stands for")
}

func TestParseIDCanonicalisesItsInput(t *testing.T) {
	lower, err := parseID(strings.ToLower(testID().String()))
	require.NoError(t, err)

	// The stored form is upper case, so a lowercased subject has to be brought
	// to it or the lookup would miss the row it names.
	assert.Equal(t, testID(), lower)
	assert.Equal(t, strings.ToUpper(testID().String()), lower.String())
}

func TestParseIDRejectsAnythingThatIsNotAnID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "garbage", raw: "not-a-number"},
		{name: "legacy integer subject", raw: "1"},
		{name: "too short", raw: "01HQZX"},
		{name: "too long", raw: "01HQZX0000000000000000000AA"},
		{name: "excluded letter", raw: "01HQZX000000000000000000IU"},
		{name: "zero", raw: "00000000000000000000000000"},
		{name: "overflowing timestamp", raw: "ZZZZZZZZZZZZZZZZZZZZZZZZZZ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := parseID(tt.raw)

			assert.ErrorIs(t, err, ErrInvalidToken)
			assert.Empty(t, id)
		})
	}
}

func TestIDString(t *testing.T) {
	// The identifier is used as a map key, a SQL parameter and a JSON value, so
	// it has to render as itself.
	assert.Equal(t, testID().String(), string(testID()))
}
