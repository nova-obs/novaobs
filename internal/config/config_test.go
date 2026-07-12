package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOverridesNestedConfigFromEnvironment(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`server:
  host: "127.0.0.1"
  port: 8080
  mode: "debug"
database:
  driver: "mongodb"
  uri: ""
secret:
  key: ""
`), 0o600))
	t.Setenv("OBS_PLATFORM_SERVER_HOST", "0.0.0.0")
	t.Setenv("OBS_PLATFORM_SERVER_MODE", "release")
	t.Setenv("OBS_PLATFORM_DATABASE_URI", "mongodb://mongo:27017/novaapm")
	t.Setenv("NOVAAPM_SECRET_KEY", "12345678901234567890123456789012")

	cfg, err := Load(configPath)

	require.NoError(t, err)
	require.Equal(t, "0.0.0.0", cfg.Server.Host)
	require.Equal(t, "release", cfg.Server.Mode)
	require.Equal(t, "mongodb://mongo:27017/novaapm", cfg.Database.URI)
	require.Equal(t, "12345678901234567890123456789012", cfg.Secret.Key)
}

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
