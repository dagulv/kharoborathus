// Package password hashes in the format the web app's password.ts wrote: hex salt, a colon, hex scrypt key.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// Node's scrypt defaults, which the stored hashes were made with.
const (
	n      = 16384
	r      = 8
	p      = 1
	keyLen = 64
)

func Hash(pw string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)

	key, err := scrypt.Key([]byte(pw), salt, n, r, p, keyLen)
	if err != nil {
		return "", fmt.Errorf("err derive key: %w", err)
	}

	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(key), nil
}

func Verify(pw, stored string) (bool, error) {
	saltHex, keyHex, ok := strings.Cut(stored, ":")
	if !ok {
		return false, errors.New("err parse hash: no colon")
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false, fmt.Errorf("err parse salt: %w", err)
	}

	want, err := hex.DecodeString(keyHex)
	if err != nil {
		return false, fmt.Errorf("err parse key: %w", err)
	}

	got, err := scrypt.Key([]byte(pw), salt, n, r, p, keyLen)
	if err != nil {
		return false, fmt.Errorf("err derive key: %w", err)
	}

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
