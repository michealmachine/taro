package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	// 创建临时配置文件
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	configContent := `
server:
  port: 8080
  db_path: /data/test.db

logging:
  level: info
  format: text

bangumi:
  uid: 123456
  username: test
  access_token: token
  refresh_token: refresh
  token_expires_at: 2026-01-01T00:00:00Z
  poll_interval: 24h

trakt:
  client_id: client
  client_secret: secret
  access_token: token
  refresh_token: refresh
  token_expires_at: 2026-01-01T00:00:00Z
  poll_interval: 24h

prowlarr:
  url: http://localhost:9696
  api_key: test_key

pikpak:
  username: test@example.com
  password: password
  poll_interval: 5m
  gc_interval: 24h
  gc_retention_days: 7

transfer:
  url: https://test.hf.space
  token: test_token
  poll_interval: 2m

telegram:
  bot_token: test:token
  chat_id: 123456789

onedrive:
  mount_path: /mnt/onedrive
  media_root: /mnt/onedrive/media
  health_check_interval: 10m

defaults:
  resolution: 1080p
  ask_mode: false
  selection_timeout: 24h
  max_concurrent_searches: 3

retention:
  state_logs_days: 90
  clean_resources_on_complete: true
`

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	// 测试加载配置
	cfg, err := Load(tmpfile.Name())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// 验证配置值
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %s, want info", cfg.Logging.Level)
	}
	if cfg.Prowlarr.URL != "http://localhost:9696" {
		t.Errorf("Prowlarr.URL = %s, want http://localhost:9696", cfg.Prowlarr.URL)
	}
	if cfg.Defaults.MaxConcurrentSearches != 3 {
		t.Errorf("Defaults.MaxConcurrentSearches = %d, want 3", cfg.Defaults.MaxConcurrentSearches)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				Server: ServerConfig{
					Port:   8080,
					DBPath: "/data/test.db",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "text",
				},
				Prowlarr: ProwlarrConfig{
					URL:    "http://localhost:9696",
					APIKey: "test",
				},
				PikPak: PikPakConfig{
					Username: "test@example.com",
					Password: "password",
				},
				Transfer: TransferConfig{
					URL:   "https://test.hf.space",
					Token: "token",
				},
				OneDrive: OneDriveConfig{
					MountPath: "/mnt/onedrive",
					MediaRoot: "/mnt/onedrive/media",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid port",
			config: Config{
				Server: ServerConfig{
					Port:   0,
					DBPath: "/data/test.db",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "text",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid logging level",
			config: Config{
				Server: ServerConfig{
					Port:   8080,
					DBPath: "/data/test.db",
				},
				Logging: LoggingConfig{
					Level:  "invalid",
					Format: "text",
				},
			},
			wantErr: true,
		},
		{
			name: "missing prowlarr url",
			config: Config{
				Server: ServerConfig{
					Port:   8080,
					DBPath: "/data/test.db",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "text",
				},
				Prowlarr: ProwlarrConfig{
					APIKey: "test",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUpdateTokens(t *testing.T) {
	// 创建临时配置文件
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	configContent := `
server:
  port: 8080
  db_path: /data/test.db
logging:
  level: info
  format: text
prowlarr:
  url: http://localhost:9696
  api_key: test
pikpak:
  username: test@example.com
  password: password
transfer:
  url: https://test.hf.space
  token: token
onedrive:
  mount_path: /mnt/onedrive
  media_root: /mnt/onedrive/media
bangumi:
  access_token: old_token
  refresh_token: old_refresh
trakt:
  access_token: old_token
  refresh_token: old_refresh
`

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	cfg, err := Load(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// 测试更新 Bangumi token
	newExpiry := time.Now().Add(24 * time.Hour)
	err = cfg.UpdateBangumiToken("new_access", "new_refresh", newExpiry)
	if err != nil {
		t.Errorf("UpdateBangumiToken() error = %v", err)
	}

	if cfg.Bangumi.AccessToken != "new_access" {
		t.Errorf("Bangumi.AccessToken = %s, want new_access", cfg.Bangumi.AccessToken)
	}

	// 测试更新 Trakt token
	err = cfg.UpdateTraktToken("new_access", "new_refresh", newExpiry)
	if err != nil {
		t.Errorf("UpdateTraktToken() error = %v", err)
	}

	if cfg.Trakt.AccessToken != "new_access" {
		t.Errorf("Trakt.AccessToken = %s, want new_access", cfg.Trakt.AccessToken)
	}
}
