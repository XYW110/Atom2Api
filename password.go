package main

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func normalizeAdminPassword(password string) (string, bool, error) {
	if strings.TrimSpace(password) == "" {
		return "", false, errors.New("admin_password must not be empty")
	}
	if adminPasswordIsHash(password) {
		return password, false, nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if errors.Is(err, bcrypt.ErrPasswordTooLong) {
		return "", false, errors.New("admin_password must not exceed 72 bytes")
	}
	if err != nil {
		return "", false, fmt.Errorf("hash admin_password: %w", err)
	}
	return string(hash), true, nil
}

func adminPasswordIsHash(password string) bool {
	_, err := bcrypt.Cost([]byte(password))
	return err == nil
}

func adminPasswordMatches(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func defaultAdminPasswordActive(hash string) bool {
	return adminPasswordMatches(hash, defaultAdminPassword)
}
