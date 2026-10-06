package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

const (
	// MinPasswordLength is the minimum allowed password length
	MinPasswordLength = 8
	// BcryptCost is the cost factor for bcrypt hashing (10 is default, 12 is more secure but slower)
	BcryptCost = 10
)

var (
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrInvalidPassword  = errors.New("invalid password")
)

// HashPassword hashes a plain text password using bcrypt
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", err
	}

	return string(hashedBytes), nil
}

// HashCode hashes a short one-time code (e.g. a 6-digit password-reset code).
//
// It is HashPassword without the length check: a 6-character code would be
// rejected by MinPasswordLength, but bcrypt is still the right primitive here
// precisely because the code is low-entropy — a fast hash of six digits is
// trivially reversible if the database leaks.
func HashCode(code string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(code), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}

// VerifyCode compares a plain one-time code with its bcrypt hash.
func VerifyCode(hashedCode, code string) error {
	return VerifyPassword(hashedCode, code)
}

// VerifyPassword compares a plain text password with a hashed password
func VerifyPassword(hashedPassword, password string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return ErrInvalidPassword
		}
		return err
	}
	return nil
}
