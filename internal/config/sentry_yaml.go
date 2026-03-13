package config

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type sentryConfigYAML struct {
	TokenRef     string   `yaml:"token_ref"`
	BaseURL      string   `yaml:"base_url"`
	Organization string   `yaml:"organization"`
	Projects     []string `yaml:"projects"`
}

func (c *SentryConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw sentryConfigYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*c = SentryConfig{
		TokenRef:        raw.TokenRef,
		BaseURL:         raw.BaseURL,
		BaseURLExplicit: sentryBaseURLExplicit(value),
		Organization:    raw.Organization,
		Projects:        raw.Projects,
	}
	return nil
}

func sentryBaseURLExplicit(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key.Value != "base_url" {
			continue
		}
		return strings.TrimSpace(value.Value) != ""
	}
	return false
}
