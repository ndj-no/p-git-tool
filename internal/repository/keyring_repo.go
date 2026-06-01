package repository

import (
	"errors"
	"fmt"
	"github.com/zalando/go-keyring"
)

const KeyringService = "workspace-tool"

// KeyringRepository wraps the zalando/go-keyring package to securely manage tokens in the OS Keyring.
type KeyringRepository struct{}

// NewKeyringRepository initializes a new KeyringRepository.
func NewKeyringRepository() *KeyringRepository {
	return &KeyringRepository{}
}

// SaveToken securely writes a PAT/Password to the keyring.
func (k *KeyringRepository) SaveToken(profileID string, token string) error {
	if profileID == "" {
		return errors.New("profile ID cannot be empty")
	}
	if token == "" {
		return errors.New("token cannot be empty")
	}
	return keyring.Set(KeyringService, profileID, token)
}

// GetToken securely retrieves a PAT/Password from the keyring.
func (k *KeyringRepository) GetToken(profileID string) (string, error) {
	if profileID == "" {
		return "", errors.New("profile ID cannot be empty")
	}
	token, err := keyring.Get(KeyringService, profileID)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("token not found for profile: %s", profileID)
		}
		return "", err
	}
	return token, nil
}

// DeleteToken securely deletes a PAT/Password from the keyring.
func (k *KeyringRepository) DeleteToken(profileID string) error {
	if profileID == "" {
		return errors.New("profile ID cannot be empty")
	}
	err := keyring.Delete(KeyringService, profileID)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil // If already deleted or not found, treat as success
		}
		return err
	}
	return nil
}
