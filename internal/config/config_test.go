package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRejectsUnsupportedDriver(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Host: "127.0.0.1", Port: 8080, Mode: "debug"},
		Database: DatabaseConfig{Driver: "sqlite", URI: "demo.db"},
		Secret:   SecretConfig{Key: "12345678901234567890123456789012"},
	}

	require.ErrorContains(t, cfg.Validate(), "mongodb")
}

func TestValidateRejectsMissingSecretKey(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Host: "127.0.0.1", Port: 8080, Mode: "debug"},
		Database: DatabaseConfig{Driver: "mongodb", URI: "mongodb://localhost:27017"},
	}

	require.ErrorContains(t, cfg.Validate(), "NOVAAPM_SECRET_KEY")
}

func TestValidateRejectsShortSecretKey(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Host: "127.0.0.1", Port: 8080, Mode: "debug"},
		Database: DatabaseConfig{Driver: "mongodb", URI: "mongodb://localhost:27017"},
		Secret:   SecretConfig{Key: "short"},
	}

	require.ErrorContains(t, cfg.Validate(), "32 字节")
}

func TestValidateAcceptsMongoConfig(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Host: "127.0.0.1", Port: 8080, Mode: "release"},
		Database: DatabaseConfig{Driver: "mongodb", URI: "mongodb://localhost:27017"},
		Secret:   SecretConfig{Key: "12345678901234567890123456789012"},
	}

	require.NoError(t, cfg.Validate())
}
