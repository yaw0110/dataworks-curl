package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	BFFBase           string `json:"bff_base,omitempty"`
	Cookie            string `json:"cookie"`
	CSRF              string `json:"csrf_token"`
	ProjectID         int64  `json:"project_id"`
	DataSourceID      int64  `json:"data_source_id"`
	ResourceGroupCode string `json:"resource_group_code"`
	AppName           string `json:"app_name,omitempty"`
	FileID            string `json:"file_id,omitempty"`
	FileName          string `json:"file_name,omitempty"`
	CU                string `json:"cu,omitempty"`
}

func configPath() (string, error) {
	path := os.Getenv("DATAWORKS_CLI_CONFIG")
	if path == "" {
		path = os.Getenv("QDM_DATAWORKS_CLI_CONFIG")
	}
	if path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dataworks-cli", "config.json"), nil
}

func defaultConfig() Config {
	return Config{
		BFFBase:           defaultBFFBase,
		ProjectID:         defaultProjectID,
		DataSourceID:      defaultDataSourceID,
		ResourceGroupCode: defaultResourceGroup,
		AppName:           defaultAppName,
		CU:                defaultCU,
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return normalizeConfig(cfg), nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return normalizeConfig(cfg), nil
}

func saveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cfg = normalizeConfig(cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func normalizeConfig(cfg Config) Config {
	d := defaultConfig()
	if cfg.BFFBase == "" {
		cfg.BFFBase = d.BFFBase
	}
	if cfg.ProjectID == 0 {
		cfg.ProjectID = d.ProjectID
	}
	if cfg.DataSourceID == 0 {
		cfg.DataSourceID = d.DataSourceID
	}
	if cfg.ResourceGroupCode == "" {
		cfg.ResourceGroupCode = d.ResourceGroupCode
	}
	if cfg.AppName == "" {
		cfg.AppName = d.AppName
	}
	if cfg.CU == "" {
		cfg.CU = d.CU
	}
	cfg.BFFBase = strings.TrimRight(cfg.BFFBase, "/")
	if cfg.CSRF == "" && cfg.Cookie != "" {
		cfg.CSRF = csrfFromCookie(cfg.Cookie)
	}
	return cfg
}
