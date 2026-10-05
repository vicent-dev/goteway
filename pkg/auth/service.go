package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"goteway/pkg/repo"
)

const (
	minPasswordLength = 8
	maxPasswordLength = 72

	maxUsernameLength = 30
)

var errTokenReused = errors.New("auth: refresh token already used")

var (
	emailPattern    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
)

type RegisterInput struct {
	Email             string
	Username          string
	Password          string
	RegistrationToken string
	RequestMeta       RequestMeta
}

type LoginInput struct {
	Email       string
	Password    string
	RequestMeta RequestMeta
}

type RefreshInput struct {
	RefreshToken string
	RequestMeta  RequestMeta
}

type LogoutInput struct {
	RefreshToken string
}

type IssueRegistrationTokenInput struct {
	IssuedBy string
	TTL      time.Duration
}

type Service struct {
	cfg    Config
	store  Store
	issuer *Issuer
	now    func() time.Time
	newID  func() (ID, error)
}

func NewService(cfg Config, store Store, issuer *Issuer) *Service {
	if issuer == nil {
		issuer = NewIssuer(cfg)
	}
	svc := &Service{
		cfg:    cfg.withDefaults(),
		store:  store,
		issuer: issuer,
		now:    time.Now,
	}

	svc.newID = func() (ID, error) { return newID(svc.now()) }
	return svc
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (*Session, error) {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}

	username, err := normalizeUsername(in.Username)
	if err != nil {
		return nil, err
	}

	if err := validatePassword(in.Password); err != nil {
		return nil, err
	}

	rawToken := strings.TrimSpace(in.RegistrationToken)
	if rawToken == "" {
		return nil, fmt.Errorf("%w: registration_token is required", ErrInvalidInput)
	}

	now := s.now()
	var session *Session

	err = s.store.WithinTx(ctx, func(tx Store) error {
		regToken, err := tx.ByTokenHash(ctx, HashToken(rawToken))
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return ErrRegistrationTokenInvalid
			}
			return err
		}
		if regToken.IsUsed() {
			return ErrRegistrationTokenInvalid
		}
		if regToken.IsExpired(now) {
			return ErrRegistrationTokenExpired
		}

		if _, err := tx.ByEmail(ctx, email); err == nil {
			return ErrEmailTaken
		} else if !errors.Is(err, repo.ErrNotFound) {
			return err
		}

		hash, err := HashPassword(in.Password, s.cfg.BcryptCost)
		if err != nil {
			return err
		}

		id, err := s.newID()
		if err != nil {
			return err
		}

		u := &User{
			ID:           id,
			Email:        email,
			Username:     username,
			PasswordHash: hash,
			Role:         RoleUser,
			IsActive:     true,
		}
		if err := tx.CreateUser(ctx, u); err != nil {
			return err
		}

		consumed, err := tx.ConsumeRegistrationToken(ctx, regToken.ID, u.ID, now)
		if err != nil {
			return err
		}
		if !consumed {
			return ErrRegistrationTokenInvalid
		}

		session, _, err = s.issue(ctx, tx, u.ID, in.RequestMeta, now)
		if err != nil {
			return err
		}
		session.User = u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) Login(ctx context.Context, in LoginInput) (*Session, error) {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	if in.Password == "" {
		return nil, fmt.Errorf("%w: password is required", ErrInvalidInput)
	}

	u, err := s.store.ByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if !u.CanAuthenticate() {
		return nil, ErrUserInactive
	}
	if !u.VerifyPassword(in.Password) {
		return nil, ErrInvalidCredentials
	}

	session, _, err := s.issue(ctx, s.store, u.ID, in.RequestMeta, s.now())
	if err != nil {
		return nil, err
	}
	session.User = u
	return session, nil
}

func (s *Service) Refresh(ctx context.Context, in RefreshInput) (*Session, error) {
	raw := strings.TrimSpace(in.RefreshToken)
	if raw == "" {
		return nil, fmt.Errorf("%w: refresh_token is required", ErrInvalidInput)
	}

	claims, err := s.issuer.Parse(raw, KindRefresh)
	if err != nil {
		return nil, err
	}

	now := s.now()
	stored, err := s.store.ByJTI(ctx, claims.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrTokenRevoked
		}
		return nil, err
	}

	if stored.TokenHash != HashToken(raw) {
		return nil, ErrInvalidToken
	}
	if stored.IsRevoked() {
		return nil, s.revokeFamily(ctx, stored.UserID, now)
	}
	if stored.IsExpired(now) {
		return nil, ErrTokenExpired
	}

	subject, err := claims.UserID()
	if err != nil {
		return nil, err
	}
	if stored.UserID != subject {
		return nil, ErrInvalidToken
	}

	u, err := s.store.ByID(ctx, subject)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	if !u.CanAuthenticate() {
		return nil, ErrUserInactive
	}

	var session *Session
	err = s.store.WithinTx(ctx, func(tx Store) error {
		current, err := tx.ByJTI(ctx, claims.ID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return errTokenReused
			}
			return err
		}
		if current.IsRevoked() {
			return errTokenReused
		}

		issued, stored, err := s.issue(ctx, tx, subject, in.RequestMeta, now)
		if err != nil {
			return err
		}
		session = issued

		current.Revoke(now, stored.JTI)
		return tx.UpdateRefreshToken(ctx, current)
	})
	if errors.Is(err, errTokenReused) {
		return nil, s.revokeFamily(ctx, stored.UserID, now)
	}
	if err != nil {
		return nil, err
	}
	session.User = u
	return session, nil
}

func (s *Service) revokeFamily(ctx context.Context, userID ID, now time.Time) error {
	if err := s.store.RevokeAllByUser(ctx, userID, now); err != nil {
		return err
	}
	return ErrTokenReused
}

func (s *Service) Logout(ctx context.Context, in LogoutInput) error {
	raw := strings.TrimSpace(in.RefreshToken)
	if raw == "" {
		return nil
	}

	claims, err := s.issuer.Parse(raw, KindRefresh)
	if err != nil {
		return err
	}

	stored, err := s.store.ByJTI(ctx, claims.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrTokenRevoked
		}
		return err
	}
	if stored.TokenHash != HashToken(raw) {
		return ErrInvalidToken
	}
	if stored.IsRevoked() {
		return nil
	}

	now := s.now()
	stored.Revoke(now, "")
	return s.store.UpdateRefreshToken(ctx, stored)
}

func (s *Service) IssueRegistrationToken(ctx context.Context, in IssueRegistrationTokenInput) (string, error) {
	ttl := in.TTL
	if ttl <= 0 {
		ttl = s.cfg.RegistrationTokenTTL
	}

	raw, err := NewOpaqueToken()
	if err != nil {
		return "", err
	}

	id, err := s.newID()
	if err != nil {
		return "", err
	}

	token := &RegistrationToken{
		ID:        id,
		TokenHash: HashToken(raw),
		IssuedBy:  in.IssuedBy,
		ExpiresAt: s.now().Add(ttl),
	}
	if err := s.store.CreateRegistrationToken(ctx, token); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Service) Authenticate(bearer string) (*Principal, error) {
	claims, err := s.issuer.Parse(bearer, KindAccess)
	if err != nil {
		return nil, err
	}

	if claims.IsExpired(s.now(), s.cfg.ClockSkew) {
		return nil, ErrTokenExpired
	}

	userID, err := claims.UserID()
	if err != nil {
		return nil, err
	}

	return &Principal{
		UserID:    userID,
		TokenID:   claims.ID,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

func (s *Service) issue(ctx context.Context, store Store, userID ID, meta RequestMeta, now time.Time) (*Session, *RefreshToken, error) {
	session, refresh, err := s.issuer.Issue(userID, meta, now)
	if err != nil {
		return nil, nil, err
	}
	id, err := s.newID()
	if err != nil {
		return nil, nil, err
	}

	refresh.ID = id
	if err := store.CreateRefreshToken(ctx, refresh); err != nil {
		return nil, nil, err
	}
	return session, refresh, nil
}

func normalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return "", fmt.Errorf("%w: email is required", ErrInvalidInput)
	}
	if !emailPattern.MatchString(normalized) {
		return "", fmt.Errorf("%w: email is malformed", ErrInvalidInput)
	}
	return normalized, nil
}

func normalizeUsername(username string) (string, error) {
	trimmed := strings.TrimSpace(username)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxUsernameLength {
		return "", fmt.Errorf("%w: username must be at most %d characters", ErrInvalidInput, maxUsernameLength)
	}
	if !usernamePattern.MatchString(trimmed) {
		return "", fmt.Errorf("%w: username may only contain letters, digits, dot, dash and underscore", ErrInvalidInput)
	}
	return trimmed, nil
}

func validatePassword(password string) error {
	switch {
	case len(password) < minPasswordLength:
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, minPasswordLength)
	case len(password) > maxPasswordLength:
		return fmt.Errorf("%w: password must be at most %d characters", ErrInvalidInput, maxPasswordLength)
	}
	return nil
}
