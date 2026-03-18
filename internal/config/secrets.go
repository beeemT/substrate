package config

import (
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "substrate"

type SecretStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

type OSKeychainStore struct{}

func (OSKeychainStore) Get(key string) (string, error) {
	value, err := keyring.Get(keyringService, key)
	if err != nil {
		return "", err
	}

	return value, nil
}

func (OSKeychainStore) Set(key, value string) error {
	return keyring.Set(keyringService, key, value)
}

func (OSKeychainStore) Delete(key string) error {
	return keyring.Delete(keyringService, key)
}

func SecretKeys() map[string]string {
	return map[string]string{
		"adapters.linear.api_key": "linear.api_key",
		"adapters.gitlab.token":   "gitlab.token",
		"adapters.github.token":   "github.token",
	}
}

func LoadSecrets(cfg *Config, store SecretStore) error {
	if cfg == nil || store == nil {
		return nil
	}
	for field, key := range SecretKeys() {
		value, err := store.Get(key)
		if err != nil {
			continue
		}
		setSecretField(cfg, field, value)
	}
	if key, ok := sentryTokenKey(cfg); ok {
		value, err := store.Get(key)
		if err == nil {
			cfg.Adapters.Sentry.Token = value
		}
	}

	return nil
}

func SaveSecrets(cfg *Config, store SecretStore) error {
	if cfg == nil || store == nil {
		return nil
	}
	for field, key := range SecretKeys() {
		value := getSecretField(cfg, field)
		if strings.TrimSpace(value) == "" {
			if err := store.Delete(key); err != nil && err != keyring.ErrNotFound {
				return fmt.Errorf("delete secret %s: %w", field, err)
			}

			continue
		}
		if err := store.Set(key, value); err != nil {
			return fmt.Errorf("save secret %s: %w", field, err)
		}
		setSecretField(cfg, field, "")
	}
	if key, ok := sentryTokenKey(cfg); ok {
		value := cfg.Adapters.Sentry.Token
		if strings.TrimSpace(value) == "" {
			if err := store.Delete(key); err != nil && err != keyring.ErrNotFound {
				return fmt.Errorf("delete secret %s: %w", "adapters.sentry.token", err)
			}
		} else {
			if err := store.Set(key, value); err != nil {
				return fmt.Errorf("save secret %s: %w", "adapters.sentry.token", err)
			}
			cfg.Adapters.Sentry.Token = ""
		}
	}

	return nil
}

func sentryTokenKey(cfg *Config) (string, bool) {
	if cfg == nil {
		return "", false
	}

	return keychainSecretKey(cfg.Adapters.Sentry.TokenRef)
}

func keychainSecretKey(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "keychain:") {
		return "", false
	}
	key := strings.TrimSpace(strings.TrimPrefix(ref, "keychain:"))
	if key == "" {
		return "", false
	}

	return key, true
}

func setSecretField(cfg *Config, field, value string) {
	switch field {
	case "adapters.linear.api_key":
		cfg.Adapters.Linear.APIKey = value
	case "adapters.gitlab.token":
		cfg.Adapters.GitLab.Token = value
	case "adapters.github.token":
		cfg.Adapters.GitHub.Token = value
	case "adapters.sentry.token":
		cfg.Adapters.Sentry.Token = value
	}
}

func getSecretField(cfg *Config, field string) string {
	switch field {
	case "adapters.linear.api_key":
		return cfg.Adapters.Linear.APIKey
	case "adapters.gitlab.token":
		return cfg.Adapters.GitLab.Token
	case "adapters.github.token":
		return cfg.Adapters.GitHub.Token
	case "adapters.sentry.token":
		return cfg.Adapters.Sentry.Token
	default:
		return ""
	}
}
