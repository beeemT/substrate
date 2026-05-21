package acp

import (
	"strings"

	"github.com/beeemT/substrate/internal/config"
)

type compactStrategy struct{ command string }

func detectCompactStrategy(init initializeResponse, cfg config.ACPConfig, commands []availableCommand) compactStrategy {
	for _, cmd := range commands {
		name := strings.TrimPrefix(strings.ToLower(cmd.Name), "/")
		if name == "compact" || name == "compress" {
			return compactStrategy{command: name}
		}
	}
	for _, method := range init.AuthMethods {
		id := strings.ToLower(method.ID)
		if id == "kilo-login" {
			return compactStrategy{command: "compact"}
		}
		if id == "cursor_login" {
			return compactStrategy{command: "compress"}
		}
	}
	name := strings.ToLower(init.AgentInfo.Name + " " + init.AgentInfo.Title)
	if strings.Contains(name, "kilo") {
		return compactStrategy{command: "compact"}
	}
	if strings.Contains(name, "cursor") {
		return compactStrategy{command: "compress"}
	}
	return detectConfiguredCompactStrategy(cfg)
}

func detectConfiguredCompactStrategy(cfg config.ACPConfig) compactStrategy {
	cmd := strings.ToLower(cfg.Command)
	args := strings.ToLower(strings.Join(cfg.Args, " "))
	if strings.Contains(cmd, "kilo") || strings.Contains(args, "kilo") {
		return compactStrategy{command: "compact"}
	}
	if strings.EqualFold(cfg.Command, "agent") && strings.Contains(args, "acp") {
		return compactStrategy{command: "compress"}
	}
	if strings.Contains(cmd, "cursor") || strings.Contains(args, "cursor") {
		return compactStrategy{command: "compress"}
	}
	return compactStrategy{}
}
