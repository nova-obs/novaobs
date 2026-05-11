package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRejectsUnsupportedDriver(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Host: "127.0.0.1", Port: 8080, Mode: "debug"},
		Database: DatabaseConfig{Driver: "sqlite", URI: "demo.db"},
	}

	require.ErrorContains(t, cfg.Validate(), "mongodb")
}

func TestValidateAcceptsMongoConfig(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Host: "127.0.0.1", Port: 8080, Mode: "release"},
		Database: DatabaseConfig{Driver: "mongodb", URI: "mongodb://localhost:27017"},
	}

	require.NoError(t, cfg.Validate())
}
