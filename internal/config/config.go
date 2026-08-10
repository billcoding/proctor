package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AgentConfig controls student-side agent behavior.
type AgentConfig struct {
	ServerURL          string `json:"server_url"`
	AgentID            string `json:"agent_id"`
	AgentToken         string `json:"agent_token"` // shared secret with server (X-Agent-Token)
	StudentName        string `json:"student_name"`
	Classroom          string `json:"classroom"`
	CollectIntervalSec int    `json:"collect_interval_sec"`
	TopNProcesses      int    `json:"top_n_processes"`
	DataDir            string `json:"data_dir"`
	LogFile            string `json:"log_file"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	AutoUpdate         bool   `json:"auto_update"` // OTA self-update; default true for lab fleets
}

// ServerConfig controls the teacher console.
type ServerConfig struct {
	Listen            string `json:"listen"`
	DataDir           string `json:"data_dir"`
	DBPath            string `json:"db_path"`
	StaticDir         string `json:"static_dir"`
	AdminToken        string `json:"admin_token"`
	AgentToken        string `json:"agent_token"`         // if set, agents must send matching X-Agent-Token
	BasicAuthUser     string `json:"basic_auth_user"`     // Web/console Basic Auth user; default proctor
	BasicAuthPassword string `json:"basic_auth_password"` // empty disables Basic Auth; default proctor
	OnlineAfter       int    `json:"online_after_sec"`
}

func DefaultAgent() AgentConfig {
	return AgentConfig{
		ServerURL:          "http://127.0.0.1:8911",
		CollectIntervalSec: 5,
		TopNProcesses:      30,
		DataDir:            defaultAgentDataDir(),
		LogFile:            "",
		AutoUpdate:         true,
	}
}

func DefaultServer() ServerConfig {
	return ServerConfig{
		Listen:            ":8911",
		DataDir:           "./data",
		DBPath:            "./data/proctor.db",
		StaticDir:         "./web/static",
		AdminToken:        "proctor-admin",
		BasicAuthUser:     "proctor",
		BasicAuthPassword: "proctor",
		OnlineAfter:       45,
	}
}

func LoadAgent(path string) (AgentConfig, error) {
	cfg := DefaultAgent()
	if path == "" {
		return cfg, nil
	}
	if err := loadJSON(path, &cfg); err != nil {
		return cfg, err
	}
	if cfg.CollectIntervalSec <= 0 {
		cfg.CollectIntervalSec = 15
	}
	if cfg.TopNProcesses <= 0 {
		cfg.TopNProcesses = 30
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaultAgentDataDir()
	}
	return cfg, nil
}

func LoadServer(path string) (ServerConfig, error) {
	cfg := DefaultServer()
	if path == "" {
		return cfg, nil
	}
	if err := loadJSON(path, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8911"
	}
	if cfg.OnlineAfter <= 0 {
		cfg.OnlineAfter = 45
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "proctor.db")
	}
	return cfg, nil
}

func SaveAgent(path string, cfg AgentConfig) error {
	return saveJSON(path, cfg)
}

func SaveServer(path string, cfg ServerConfig) error {
	return saveJSON(path, cfg)
}

func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func saveJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func defaultAgentDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./data"
	}
	return filepath.Join(home, ".proctor")
}
