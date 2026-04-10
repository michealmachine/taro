package config

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// Config represents the application configuration
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	Bangumi   BangumiConfig   `mapstructure:"bangumi"`
	Trakt     TraktConfig     `mapstructure:"trakt"`
	Prowlarr  ProwlarrConfig  `mapstructure:"prowlarr"`
	PikPak    PikPakConfig    `mapstructure:"pikpak"`
	Transfer  TransferConfig  `mapstructure:"transfer"`
	Telegram  TelegramConfig  `mapstructure:"telegram"`
	OneDrive  OneDriveConfig  `mapstructure:"onedrive"`
	Defaults  DefaultsConfig  `mapstructure:"defaults"`
	Retention RetentionConfig `mapstructure:"retention"`

	mu sync.Mutex // 保护配置文件写入
}

type ServerConfig struct {
	Port   int    `mapstructure:"port"`
	DBPath string `mapstructure:"db_path"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // debug | info | warn | error
	Format string `mapstructure:"format"` // text | json
}

type BangumiConfig struct {
	UID            int       `mapstructure:"uid"`
	Username       string    `mapstructure:"username"`
	AccessToken    string    `mapstructure:"access_token"`
	RefreshToken   string    `mapstructure:"refresh_token"`
	TokenExpiresAt time.Time `mapstructure:"token_expires_at"`
	PollInterval   string    `mapstructure:"poll_interval"`
}

type TraktConfig struct {
	ClientID       string    `mapstructure:"client_id"`
	ClientSecret   string    `mapstructure:"client_secret"`
	AccessToken    string    `mapstructure:"access_token"`
	RefreshToken   string    `mapstructure:"refresh_token"`
	TokenExpiresAt time.Time `mapstructure:"token_expires_at"`
	PollInterval   string    `mapstructure:"poll_interval"`
}

type ProwlarrConfig struct {
	URL    string `mapstructure:"url"`
	APIKey string `mapstructure:"api_key"`
}

type PikPakConfig struct {
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	PollInterval    string `mapstructure:"poll_interval"`
	GCInterval      string `mapstructure:"gc_interval"`
	GCRetentionDays int    `mapstructure:"gc_retention_days"`
}

type TransferConfig struct {
	URL          string `mapstructure:"url"`
	Token        string `mapstructure:"token"`
	PollInterval string `mapstructure:"poll_interval"`
}

type TelegramConfig struct {
	BotToken string `mapstructure:"bot_token"`
	ChatID   int64  `mapstructure:"chat_id"`
}

type OneDriveConfig struct {
	MountPath           string `mapstructure:"mount_path"`
	MediaRoot           string `mapstructure:"media_root"`
	HealthCheckInterval string `mapstructure:"health_check_interval"`
}

type DefaultsConfig struct {
	Resolution            string   `mapstructure:"resolution"`
	AskMode               bool     `mapstructure:"ask_mode"`
	SelectionTimeout      string   `mapstructure:"selection_timeout"`
	MaxConcurrentSearches int      `mapstructure:"max_concurrent_searches"`
	ExcludedCodecs        []string `mapstructure:"excluded_codecs"` // e.g. ["av1", "x265"]
}

type RetentionConfig struct {
	StateLogsDays            int  `mapstructure:"state_logs_days"`
	CleanResourcesOnComplete bool `mapstructure:"clean_resources_on_complete"`
}

var (
	globalConfig *Config
	configPath   string
)

// Load loads configuration from file and environment variables
func Load(path string) (*Config, error) {
	configPath = path

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// 设置环境变量前缀和映射规则
	v.SetEnvPrefix("TARO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 验证必填配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	globalConfig = &cfg
	return &cfg, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Server
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Server.DBPath == "" {
		return fmt.Errorf("server.db_path is required")
	}

	// Logging
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid logging level: %s (must be debug|info|warn|error)", c.Logging.Level)
	}
	validFormats := map[string]bool{"text": true, "json": true}
	if !validFormats[c.Logging.Format] {
		return fmt.Errorf("invalid logging format: %s (must be text|json)", c.Logging.Format)
	}

	// Prowlarr
	if c.Prowlarr.URL == "" {
		return fmt.Errorf("prowlarr.url is required")
	}
	if c.Prowlarr.APIKey == "" {
		return fmt.Errorf("prowlarr.api_key is required")
	}

	// PikPak
	if c.PikPak.Username == "" {
		return fmt.Errorf("pikpak.username is required")
	}
	if c.PikPak.Password == "" {
		return fmt.Errorf("pikpak.password is required")
	}

	// Transfer
	if c.Transfer.URL == "" {
		return fmt.Errorf("transfer.url is required")
	}
	if c.Transfer.Token == "" {
		return fmt.Errorf("transfer.token is required")
	}

	// OneDrive
	if c.OneDrive.MountPath == "" {
		return fmt.Errorf("onedrive.mount_path is required")
	}
	if c.OneDrive.MediaRoot == "" {
		return fmt.Errorf("onedrive.media_root is required")
	}

	// Defaults
	if c.Defaults.MaxConcurrentSearches <= 0 {
		c.Defaults.MaxConcurrentSearches = 3
	}

	return nil
}

// Save saves the current configuration back to file (thread-safe)
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Read current config file to preserve unknown fields and formatting
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Try to read existing config, ignore error if file doesn't exist
	_ = v.ReadInConfig()

	// Update only the fields we manage
	v.Set("server", c.Server)
	v.Set("logging", c.Logging)
	v.Set("bangumi", c.Bangumi)
	v.Set("trakt", c.Trakt)
	v.Set("prowlarr", c.Prowlarr)
	v.Set("pikpak", c.PikPak)
	v.Set("transfer", c.Transfer)
	v.Set("telegram", c.Telegram)
	v.Set("onedrive", c.OneDrive)
	v.Set("defaults", c.Defaults)
	v.Set("retention", c.Retention)

	if err := v.WriteConfig(); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// UpdateBangumiToken updates Bangumi OAuth2 tokens and saves to file
func (c *Config) UpdateBangumiToken(accessToken, refreshToken string, expiresAt time.Time) error {
	c.Bangumi.AccessToken = accessToken
	c.Bangumi.RefreshToken = refreshToken
	c.Bangumi.TokenExpiresAt = expiresAt
	return c.Save()
}

// UpdateTraktToken updates Trakt OAuth2 tokens and saves to file
func (c *Config) UpdateTraktToken(accessToken, refreshToken string, expiresAt time.Time) error {
	c.Trakt.AccessToken = accessToken
	c.Trakt.RefreshToken = refreshToken
	c.Trakt.TokenExpiresAt = expiresAt
	return c.Save()
}

// Get returns the global configuration instance
func Get() *Config {
	return globalConfig
}

// MustLoad loads configuration or panics
func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}
