package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	ProxyKey       string
	AccountK1Key   string
	AccountK2Key   string
	AccountK3Key   string
	AffinityHMAC   string
	DBPath         string
	ListenAddr     string
	LogLevel       string
}

func LoadConfig() (*Config, error) {
	c := &Config{
		ListenAddr: "127.0.0.1:4000",
		LogLevel:   "info",
	}

	required := map[string]*string{
		"LLMUX_PROXY_KEY":         &c.ProxyKey,
		"LLMUX_ACCOUNT_K1_KEY":    &c.AccountK1Key,
		"LLMUX_ACCOUNT_K2_KEY":    &c.AccountK2Key,
		"LLMUX_ACCOUNT_K3_KEY":    &c.AccountK3Key,
		"LLMUX_AFFINITY_HMAC_KEY": &c.AffinityHMAC,
		"LLMUX_DB_PATH":           &c.DBPath,
	}

	for key, ptr := range required {
		val := os.Getenv(key)
		if val == "" {
			return nil, fmt.Errorf("missing required environment variable: %s", key)
		}
		*ptr = val
	}

	if val := os.Getenv("LLMUX_LISTEN_ADDR"); val != "" {
		c.ListenAddr = val
	}
	if val := os.Getenv("LLMUX_LOG_LEVEL"); val != "" {
		c.LogLevel = val
	}

	if !filepath.IsAbs(c.DBPath) {
		return nil, errors.New("LLMUX_DB_PATH must be an absolute path")
	}

	return c, nil
}
