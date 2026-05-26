package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds the AI-related application configuration.
type Config struct {
	OpenAIAPIKey string `json:"openai_api_key"`
	OpenAIModel  string `json:"openai_model"`
	AIEnabled    bool   `json:"ai_enabled"`
}

// GetConfigDir returns the path to the todotui config directory.
func GetConfigDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfg, "todotui")
}

// GetConfigPath returns the full path to the config.json file.
func GetConfigPath() string {
	dir := GetConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "config.json")
}

// LoadConfig reads the config file from the user config directory.
// If the file doesn't exist, returns sensible defaults.
// If the API key in config is empty, falls back to the OPENAI_API_KEY
// environment variable.
func LoadConfig() Config {
	// Defaults
	config := Config{
		OpenAIAPIKey: "",
		OpenAIModel:  "gpt-4o-mini",
		AIEnabled:    true,
	}

	path := GetConfigPath()
	if path == "" {
		// Can't determine config dir — try env var and return
		config.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
		return config
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist or can't be read — try env var
		config.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
		return config
	}

	if err := json.Unmarshal(data, &config); err != nil {
		// Invalid JSON — use defaults + env var
		config.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
		return config
	}

	// If model is empty in config, set default
	if config.OpenAIModel == "" {
		config.OpenAIModel = "gpt-4o-mini"
	}

	// Fall back to env var if config key is empty
	if config.OpenAIAPIKey == "" {
		config.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
	}

	return config
}
