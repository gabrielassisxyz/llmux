package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func validCredentialValue(key string) string {
	switch key {
	case "LLMUX_PROXY_KEY":
		return "12345678901234567890123456789012"
	case "LLMUX_AFFINITY_HMAC_KEY":
		return "22345678901234567890123456789012"
	case "LLMUX_ACCOUNT_K1_KEY":
		return "32345678901234567890123456789012"
	case "LLMUX_ACCOUNT_K2_KEY":
		return "42345678901234567890123456789012"
	case "LLMUX_ACCOUNT_K3_KEY":
		return "52345678901234567890123456789012"
	default:
		return key + "_secret"
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	required := []string{
		"LLMUX_PROXY_KEY",
		"LLMUX_ACCOUNT_K1_KEY",
		"LLMUX_ACCOUNT_K2_KEY",
		"LLMUX_ACCOUNT_K3_KEY",
		"LLMUX_AFFINITY_HMAC_KEY",
		"LLMUX_DB_PATH",
	}

	for _, req := range required {
		t.Run("Missing_"+req, func(t *testing.T) {
			// Clear all first
			for _, k := range required {
				t.Setenv(k, "")
			}
			// Set all except the one we're testing
			for _, k := range required {
				if k != req {
					switch k {
					case "LLMUX_DB_PATH":
						t.Setenv(k, "/tmp/db.sqlite")
					case "LLMUX_PROXY_KEY":
						t.Setenv(k, "12345678901234567890123456789012")
					case "LLMUX_AFFINITY_HMAC_KEY":
						t.Setenv(k, "22345678901234567890123456789012")
					default:
						t.Setenv(k, validCredentialValue(k))
					}
				}
			}

			_, err := LoadConfig()
			if err == nil {
				t.Fatalf("expected error for missing %s, got nil", req)
			}
			if !strings.Contains(err.Error(), req) {
				t.Errorf("error should name the missing variable %s, got: %v", req, err)
			}
		})
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	required := []string{
		"LLMUX_PROXY_KEY",
		"LLMUX_ACCOUNT_K1_KEY",
		"LLMUX_ACCOUNT_K2_KEY",
		"LLMUX_ACCOUNT_K3_KEY",
		"LLMUX_AFFINITY_HMAC_KEY",
	}
	for _, k := range required {
		switch k {
		case "LLMUX_PROXY_KEY":
			t.Setenv(k, "12345678901234567890123456789012")
		case "LLMUX_AFFINITY_HMAC_KEY":
			t.Setenv(k, "22345678901234567890123456789012")
		default:
			t.Setenv(k, validCredentialValue(k))
		}
	}
	t.Setenv("LLMUX_DB_PATH", "/tmp/db.sqlite")
	t.Setenv("LLMUX_LISTEN_ADDR", "")
	t.Setenv("LLMUX_LOG_LEVEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:4000" {
		t.Errorf("expected default ListenAddr 127.0.0.1:4000, got %q", cfg.ListenAddr)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel info, got %q", cfg.LogLevel)
	}
}

func TestLoadConfig_RelativeDBPath(t *testing.T) {
	required := []string{
		"LLMUX_PROXY_KEY",
		"LLMUX_ACCOUNT_K1_KEY",
		"LLMUX_ACCOUNT_K2_KEY",
		"LLMUX_ACCOUNT_K3_KEY",
		"LLMUX_AFFINITY_HMAC_KEY",
	}
	for _, k := range required {
		switch k {
		case "LLMUX_PROXY_KEY":
			t.Setenv(k, "12345678901234567890123456789012")
		case "LLMUX_AFFINITY_HMAC_KEY":
			t.Setenv(k, "22345678901234567890123456789012")
		default:
			t.Setenv(k, validCredentialValue(k))
		}
	}
	t.Setenv("LLMUX_DB_PATH", "relative/path/db.sqlite")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for relative DB path, got nil")
	}
	if !strings.Contains(err.Error(), "LLMUX_DB_PATH") {
		t.Errorf("error should mention LLMUX_DB_PATH, got: %v", err)
	}
}

func TestLoadConfig_NoSecretLeakInError(t *testing.T) {
	marker := "MARKER_SECRET_VALUE_MARKER_PAD32"
	required := []string{
		"LLMUX_PROXY_KEY",
		"LLMUX_ACCOUNT_K1_KEY",
		"LLMUX_ACCOUNT_K2_KEY",
		"LLMUX_ACCOUNT_K3_KEY",
		"LLMUX_AFFINITY_HMAC_KEY",
	}

	// Test relative path error doesn't leak secrets
	for i, k := range required {
		t.Setenv(k, fmt.Sprintf("%d_%s", i, marker))
	}
	t.Setenv("LLMUX_DB_PATH", "relative/path/db.sqlite")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), marker) {
		t.Errorf("secret marker leaked in error message: %v", err)
	}
}

func TestLoadConfig_ListenAddr(t *testing.T) {
	required := []string{
		"LLMUX_PROXY_KEY",
		"LLMUX_ACCOUNT_K1_KEY",
		"LLMUX_ACCOUNT_K2_KEY",
		"LLMUX_ACCOUNT_K3_KEY",
		"LLMUX_AFFINITY_HMAC_KEY",
	}

	tests := []struct {
		addr  string
		valid bool
	}{
		{"127.0.0.1:4000", true},
		{"[::1]:4000", true},
		{"127.0.0.2:4000", true},
		{"localhost:4000", false},
		{"0.0.0.0:4000", false},
		{"[::]:4000", false},
		{"8.8.8.8:4000", false},
		{":4000", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			for _, k := range required {
				switch k {
				case "LLMUX_PROXY_KEY":
					t.Setenv(k, "12345678901234567890123456789012")
				case "LLMUX_AFFINITY_HMAC_KEY":
					t.Setenv(k, "22345678901234567890123456789012")
				default:
					t.Setenv(k, validCredentialValue(k))
				}
			}
			t.Setenv("LLMUX_DB_PATH", "/tmp/db.sqlite")
			t.Setenv("LLMUX_LISTEN_ADDR", tt.addr)

			_, err := LoadConfig()
			if tt.valid && err != nil {
				t.Errorf("expected %q to be valid, got error: %v", tt.addr, err)
			}
			if !tt.valid && err == nil {
				t.Errorf("expected %q to be invalid, got nil error", tt.addr)
			}
		})
	}
}

func TestLoadConfig_Credentials(t *testing.T) {
	setupValid := func() {
		t.Setenv("LLMUX_DB_PATH", "/tmp/db.sqlite")
		t.Setenv("LLMUX_PROXY_KEY", "12345678901234567890123456789012") // 32 chars
		t.Setenv("LLMUX_AFFINITY_HMAC_KEY", "22345678901234567890123456789012")
		t.Setenv("LLMUX_ACCOUNT_K1_KEY", "32345678901234567890123456789012")
		t.Setenv("LLMUX_ACCOUNT_K2_KEY", "42345678901234567890123456789012")
		t.Setenv("LLMUX_ACCOUNT_K3_KEY", "52345678901234567890123456789012")
	}

	t.Run("Valid", func(t *testing.T) {
		setupValid()
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("expected valid config, got error: %v", err)
		}
		if cfg.ProxyKeyDigest == [32]byte{} {
			t.Error("expected proxy key digest to be populated")
		}
	})

	t.Run("ProxyKeyDigestStableAcrossRestarts", func(t *testing.T) {
		setupValid()
		want := sha256.Sum256([]byte("12345678901234567890123456789012"))

		first, err := LoadConfig()
		if err != nil {
			t.Fatalf("first LoadConfig() error = %v", err)
		}

		second, err := LoadConfig()
		if err != nil {
			t.Fatalf("second LoadConfig() error = %v", err)
		}

		if first.ProxyKeyDigest != second.ProxyKeyDigest {
			t.Error("same proxy key produced different digests across fresh configuration loads")
		}
		if first.ProxyKeyDigest != want || second.ProxyKeyDigest != want {
			t.Error("fresh configuration loads did not derive the proxy key digest")
		}
	})

	t.Run("ProxyKeyDigestMatchesFixedVector", func(t *testing.T) {
		setupValid()
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		const want = "e1b85b27d6bcb05846c18e6a48f118e89f0c0587140de9fb3359f8370d0dba08"
		if got := hex.EncodeToString(cfg.ProxyKeyDigest[:]); got != want {
			t.Errorf("proxy key digest = %s, want fixed vector %s", got, want)
		}
	})

	t.Run("ProxyKeyMustDifferFromAccountKey", func(t *testing.T) {
		setupValid()
		t.Setenv("LLMUX_ACCOUNT_K1_KEY", "12345678901234567890123456789012")
		if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "LLMUX_PROXY_KEY") || !strings.Contains(err.Error(), "LLMUX_ACCOUNT_K1_KEY") {
			t.Errorf("expected proxy/account equality error, got %v", err)
		}
	})

	for _, key := range []string{"LLMUX_ACCOUNT_K1_KEY", "LLMUX_ACCOUNT_K2_KEY", "LLMUX_ACCOUNT_K3_KEY"} {
		t.Run("ShortUpstreamKey_"+key, func(t *testing.T) {
			setupValid()
			t.Setenv(key, strings.Repeat("x", 31))
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), key+" must be at least 32 bytes") {
				t.Errorf("expected upstream key length error, got %v", err)
			}
		})
	}

	t.Run("ShortProxyKey", func(t *testing.T) {
		setupValid()
		t.Setenv("LLMUX_PROXY_KEY", "short")
		_, err := LoadConfig()
		if err == nil || !strings.Contains(err.Error(), "LLMUX_PROXY_KEY must be at least 32 bytes") {
			t.Errorf("expected length error, got: %v", err)
		}
	})

	t.Run("ShortAffinityKey", func(t *testing.T) {
		setupValid()
		t.Setenv("LLMUX_AFFINITY_HMAC_KEY", "short")
		_, err := LoadConfig()
		if err == nil || !strings.Contains(err.Error(), "LLMUX_AFFINITY_HMAC_KEY must be at least 32 bytes") {
			t.Errorf("expected length error, got: %v", err)
		}
	})

	t.Run("DuplicateKeys", func(t *testing.T) {
		setupValid()
		t.Setenv("LLMUX_ACCOUNT_K1_KEY", "same-secret-value-that-is-32-bytes")
		t.Setenv("LLMUX_ACCOUNT_K2_KEY", "same-secret-value-that-is-32-bytes")
		_, err := LoadConfig()
		if err == nil || !strings.Contains(err.Error(), "must not be equal") {
			t.Errorf("expected duplicate key error, got: %v", err)
		}
	})

	t.Run("EmptyKey", func(t *testing.T) {
		setupValid()
		t.Setenv("LLMUX_ACCOUNT_K3_KEY", "")
		_, err := LoadConfig()
		if err == nil || (!strings.Contains(err.Error(), "must not be empty") && !strings.Contains(err.Error(), "missing required")) {
			t.Errorf("expected empty key error, got: %v", err)
		}
	})
}
