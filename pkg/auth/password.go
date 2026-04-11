package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

const (
	// MinPasswordLength is the minimum allowed password length
	MinPasswordLength = 8
	// BcryptCost is the cost factor for bcrypt hashing (10 is default, 12 is more secure but slower)
	BcryptCost = 12
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
