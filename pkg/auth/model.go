package auth

import (
	"time"

	"gorm.io/gorm"
)

type Role string

const (
	// RoleUser is the role given to every self registered account.
	RoleUser Role = "user"
	// RoleAdmin is reserved for accounts allowed to manage other accounts.
	RoleAdmin Role = "admin"
)

type User struct {
	ID           ID             `gorm:"type:varchar(26);primaryKey" json:"id"`
	Email        string         `gorm:"type:varchar(255);uniqueIndex;not null" json:"email"`
	Username     string         `gorm:"type:varchar(100);uniqueIndex" json:"username,omitempty"`
	PasswordHash string         `gorm:"type:varchar(255);not null" json:"-"`
	Role         Role           `gorm:"type:varchar(50);default:'user';not null" json:"role"`
	IsActive     bool           `gorm:"default:true;not null" json:"is_active"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (u *User) VerifyPassword(plain string) bool {
	return VerifyPassword(u.PasswordHash, plain)
}

func (u *User) CanAuthenticate() bool {
	return u != nil && u.IsActive
}

type RefreshToken struct {
	ID            ID         `gorm:"type:varchar(26);primaryKey"`
	JTI           string     `gorm:"type:varchar(64);uniqueIndex;not null"`
	UserID        ID         `gorm:"type:varchar(26);index;not null"`
	TokenHash     string     `gorm:"type:varchar(255);index;not null"`
	ExpiresAt     time.Time  `gorm:"index;not null"`
	RevokedAt     *time.Time `gorm:"index"`
	ReplacedByJTI string     `gorm:"type:varchar(64);index"`
	UserAgent     string     `gorm:"type:varchar(255)"`
	IP            string     `gorm:"type:varchar(45)"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

func (t *RefreshToken) IsRevoked() bool {
	return t.RevokedAt != nil
}

func (t *RefreshToken) IsExpired(now time.Time) bool {
	return !t.ExpiresAt.After(now)
}

func (t *RefreshToken) Revoke(now time.Time, replacedBy string) {
	t.RevokedAt = &now
	t.ReplacedByJTI = replacedBy
}

type RegistrationToken struct {
	ID           ID         `gorm:"type:varchar(26);primaryKey"`
	TokenHash    string     `gorm:"type:varchar(255);uniqueIndex;not null"`
	IssuedBy     string     `gorm:"type:varchar(100)"`
	ExpiresAt    time.Time  `gorm:"not null"`
	UsedAt       *time.Time `gorm:"index"`
	UsedByUserID *ID        `gorm:"type:varchar(26);index"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

func (t *RegistrationToken) IsUsed() bool {
	return t.UsedAt != nil
}

func (t *RegistrationToken) IsExpired(now time.Time) bool {
	return !t.ExpiresAt.After(now)
}
