package acp

import (
	"encoding/json"
	"fmt"
	"runtime"
)

type Registry struct {
	Agents []RegistryAgent `json:"agents"`
}

type RegistryAgent struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Distributions []RegistryDistribution `json:"distributions"`
}

type RegistryDistribution struct {
	Type     string            `json:"type"`
	Command  string            `json:"command"`
	Package  string            `json:"package"`
	Args     []string          `json:"args"`
	Binaries map[string]string `json:"binaries"`
}

func ParseRegistry(data []byte) (Registry, error) {
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("parse ACP registry: %w", err)
	}
	return reg, nil
}

func (a RegistryAgent) DistributionForCurrentPlatform() (RegistryDistribution, bool) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	// First pass: find a binary distribution with a matching platform.
	for _, dist := range a.Distributions {
		if dist.Type == "binary" && len(dist.Binaries) > 0 {
			if _, ok := dist.Binaries[platform]; ok {
				return dist, true
			}
		}
	}
	// Second pass: return the first non-binary distribution as a fallback.
	// Non-binary distributions (npm, brew, etc.) are installable on any platform
	// but lack the fine-grained binary selection, so they are a last resort.
	for _, dist := range a.Distributions {
		if dist.Type != "binary" {
			return dist, true
		}
	}
	return RegistryDistribution{}, false
}
