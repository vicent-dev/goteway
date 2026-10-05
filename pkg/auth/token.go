package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type TokenKind string

const (
	// KindAccess identifies short lived tokens that authenticate callers.
	KindAccess TokenKind = "access"
	// KindRefresh identifies the tokens that can mint new sessions.
	KindRefresh TokenKind = "refresh"
)

// BearerScheme is the Authorization scheme of the tokens this package issues.
const BearerScheme = "Bearer"

// Claims are the claims carried by the tokens issued by the Issuer.
type Claims struct {
	jwt.RegisteredClaims
	// Kind is the token kind, kept under the typ claim name.
	Kind TokenKind `json:"typ"`
}

func (c *Claims) UserID() (ID, error) {
	return parseID(c.Subject)
}

func (c *Claims) IsExpired(now time.Time, leeway time.Duration) bool {
	if c.ExpiresAt == nil {
		return true
	}
	return !now.Before(c.ExpiresAt.Add(leeway))
}

// RequestMeta is the client metadata stored alongside an issued session.
type RequestMeta struct {
	UserAgent string
	IP        string
}

type Session struct {
	AccessToken  string
	RefreshToken string
	Scheme       string

	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time

	User *User
}

// Issuer signs and verifies the gateway JWTs. It owns no persistence: turning
// a refresh token into a stored session is the service's job, which keeps the
// signing code a pure function of its configuration.
type Issuer struct {
	cfg Config
}

func NewIssuer(cfg Config) *Issuer {
	return &Issuer{cfg: cfg.withDefaults()}
}

func (i *Issuer) Issue(userID ID, meta RequestMeta, now time.Time) (*Session, *RefreshToken, error) {
	accessExpiresAt := now.Add(i.cfg.AccessTTL)
	accessToken, _, err := i.sign(userID, KindAccess, i.cfg.AccessSecret, accessExpiresAt, now)
	if err != nil {
		return nil, nil, err
	}

	refreshExpiresAt := now.Add(i.cfg.RefreshTTL)
	refreshToken, refreshJTI, err := i.sign(userID, KindRefresh, i.cfg.RefreshSecret, refreshExpiresAt, now)
	if err != nil {
		return nil, nil, err
	}

	session := &Session{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		Scheme:           BearerScheme,
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}

	stored := &RefreshToken{
		JTI:       refreshJTI,
		UserID:    userID,
		TokenHash: HashToken(refreshToken),
		ExpiresAt: refreshExpiresAt,
		UserAgent: meta.UserAgent,
		IP:        meta.IP,
	}

	return session, stored, nil
}

func (i *Issuer) Parse(raw string, kind TokenKind) (*Claims, error) {
	if raw == "" {
		return nil, ErrMissingToken
	}

	secret := i.cfg.AccessSecret
	if kind == KindRefresh {
		secret = i.cfg.RefreshSecret
	}

	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims,
		func(*jwt.Token) (any, error) { return []byte(secret), nil },
		// Pinning the method keeps a token signed with another algorithm from
		// ever reaching the key function.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithLeeway(i.cfg.ClockSkew),
		jwt.WithIssuer(i.cfg.Issuer),
		jwt.WithAudience(i.cfg.Audience),
	)
	if err != nil {
		return nil, parseError(err)
	}
	if claims.Kind != kind {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (i *Issuer) ParseUserID(raw string, kind TokenKind) (ID, error) {
	claims, err := i.Parse(raw, kind)
	if err != nil {
		return "", err
	}
	return claims.UserID()
}

func (i *Issuer) sign(userID ID, kind TokenKind, secret string, expiresAt, now time.Time) (raw, jti string, err error) {
	jti, err = newJTI(now)
	if err != nil {
		return "", "", err
	}

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    i.cfg.Issuer,
			Audience:  jwt.ClaimStrings{i.cfg.Audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        jti,
		},
		Kind: kind,
	}

	raw, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", "", err
	}
	return raw, jti, nil
}

func parseError(err error) error {
	if errors.Is(err, jwt.ErrTokenExpired) {
		return ErrTokenExpired
	}
	return ErrInvalidToken
}
