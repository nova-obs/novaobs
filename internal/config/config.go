package config

import (
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

type Config struct {
	Server            ServerConfig   `mapstructure:"server"`
	Database          DatabaseConfig `mapstructure:"database"`
	Secret            SecretConfig   `mapstructure:"secret"`
	CollectorTemplate string         `mapstructure:"collector_template"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

type DatabaseConfig struct {
	Driver string `mapstructure:"driver"`
	URI    string `mapstructure:"uri"`
}

type SecretConfig struct {
	Key string `mapstructure:"key"`
}

func Load(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("OBS_PLATFORM")
	v.AutomaticEnv()

	var cfg Config
	if err := v.ReadInConfig(); err != nil {
		return cfg, err
	}
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}
	if cfg.Secret.Key == "" {
		cfg.Secret.Key = os.Getenv("NOVAOBS_SECRET_KEY")
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 必须在 1 到 65535 之间")
	}
	if c.Server.Mode != gin.DebugMode && c.Server.Mode != gin.ReleaseMode && c.Server.Mode != gin.TestMode {
		return fmt.Errorf("server.mode 只支持 debug、release 或 test")
	}
	if c.Database.Driver != "mongodb" {
		return fmt.Errorf("database.driver 当前只支持 mongodb")
	}
	if c.Database.URI == "" {
		return fmt.Errorf("database.uri 不能为空")
	}
	if c.Secret.Key == "" {
		return fmt.Errorf("NOVAOBS_SECRET_KEY 不能为空")
	}
	if len([]byte(c.Secret.Key)) != 32 {
		return fmt.Errorf("NOVAOBS_SECRET_KEY 长度必须为 32 字节")
	}
	return nil
}
