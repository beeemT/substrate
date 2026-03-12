package config

import (
	"testing"

	"github.com/zalando/go-keyring"
)

type memorySecretStore struct {
	values  map[string]string
	deleted []string
}

func (s *memorySecretStore) Get(key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", keyring.ErrNotFound
}

func (s *memorySecretStore) Set(key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}

func (s *memorySecretStore) Delete(key string) error {
	s.deleted = append(s.deleted, key)
	delete(s.values, key)
	return nil
}

func TestLoadSecretsHydratesSentryDefaultTokenRef(t *testing.T) {
	cfg := &Config{}
	cfg.Adapters.Sentry.TokenRef = "keychain:sentry.token"
	store := &memorySecretStore{values: map[string]string{"sentry.token": "secret-token"}}

	if err := LoadSecrets(cfg, store); err != nil {
		t.Fatalf("LoadSecrets() error = %v", err)
	}
	if cfg.Adapters.Sentry.Token != "secret-token" {
		t.Fatalf("cfg.Adapters.Sentry.Token = %q, want %q", cfg.Adapters.Sentry.Token, "secret-token")
	}
}

func TestLoadSecretsHydratesSentryCustomTokenRef(t *testing.T) {
	cfg := &Config{}
	cfg.Adapters.Sentry.TokenRef = "keychain:custom.sentry.token"
	store := &memorySecretStore{values: map[string]string{
		"sentry.token":        "wrong-token",
		"custom.sentry.token": "secret-token",
	}}

	if err := LoadSecrets(cfg, store); err != nil {
		t.Fatalf("LoadSecrets() error = %v", err)
	}
	if cfg.Adapters.Sentry.Token != "secret-token" {
		t.Fatalf("cfg.Adapters.Sentry.Token = %q, want %q", cfg.Adapters.Sentry.Token, "secret-token")
	}
}

func TestLoadSecretsSkipsSentryWithoutTokenRef(t *testing.T) {
	cfg := &Config{}
	store := &memorySecretStore{values: map[string]string{"sentry.token": "secret-token"}}

	if err := LoadSecrets(cfg, store); err != nil {
		t.Fatalf("LoadSecrets() error = %v", err)
	}
	if cfg.Adapters.Sentry.Token != "" {
		t.Fatalf("cfg.Adapters.Sentry.Token = %q, want empty without token_ref", cfg.Adapters.Sentry.Token)
	}
}

func TestLoadSecretsSkipsUnsupportedSentryTokenRef(t *testing.T) {
	cfg := &Config{}
	cfg.Adapters.Sentry.TokenRef = "env:SENTRY_TOKEN"
	store := &memorySecretStore{values: map[string]string{"sentry.token": "secret-token"}}

	if err := LoadSecrets(cfg, store); err != nil {
		t.Fatalf("LoadSecrets() error = %v", err)
	}
	if cfg.Adapters.Sentry.Token != "" {
		t.Fatalf("cfg.Adapters.Sentry.Token = %q, want empty for unsupported token_ref", cfg.Adapters.Sentry.Token)
	}
}

func TestSaveSecretsPersistsSentryTokenToConfiguredRef(t *testing.T) {
	cfg := &Config{}
	cfg.Adapters.Sentry.TokenRef = "keychain:custom.sentry.token"
	cfg.Adapters.Sentry.Token = "secret-token"
	store := &memorySecretStore{}

	if err := SaveSecrets(cfg, store); err != nil {
		t.Fatalf("SaveSecrets() error = %v", err)
	}
	if got := store.values["custom.sentry.token"]; got != "secret-token" {
		t.Fatalf("store.values[%q] = %q, want %q", "custom.sentry.token", got, "secret-token")
	}
	if _, ok := store.values["sentry.token"]; ok {
		t.Fatal("store.values should not write sentry.token when token_ref points elsewhere")
	}
	if cfg.Adapters.Sentry.Token != "" {
		t.Fatalf("cfg.Adapters.Sentry.Token = %q, want empty after SaveSecrets", cfg.Adapters.Sentry.Token)
	}
}

func TestSaveSecretsDeletesBlankSentryToken(t *testing.T) {
	cfg := &Config{}
	cfg.Adapters.Sentry.TokenRef = "keychain:sentry.token"
	store := &memorySecretStore{values: map[string]string{"sentry.token": "secret-token"}}

	if err := SaveSecrets(cfg, store); err != nil {
		t.Fatalf("SaveSecrets() error = %v", err)
	}
	if _, ok := store.values["sentry.token"]; ok {
		t.Fatal("store.values should not retain sentry.token after deleting blank secret")
	}
	found := false
	for _, key := range store.deleted {
		if key == "sentry.token" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("store.deleted = %#v, want delete for %q", store.deleted, "sentry.token")
	}
}
