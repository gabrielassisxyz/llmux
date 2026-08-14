package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMainStartup(t *testing.T) {
	if os.Getenv("BE_CRASHER") == "1" {
		main()
		return
	}

	marker := "MARKER_SECRET_VALUE_MARKER_PAD32"
	required := []string{
		"LLMUX_PROXY_KEY",
		"LLMUX_ACCOUNT_K1_KEY",
		"LLMUX_ACCOUNT_K2_KEY",
		"LLMUX_ACCOUNT_K3_KEY",
		"LLMUX_AFFINITY_HMAC_KEY",
	}

	// Test relative path error doesn't leak secrets in main's stderr
	cmd := exec.Command(os.Args[0], "-test.run=TestMainStartup")

	env := os.Environ()
	env = append(env, "BE_CRASHER=1")
	for i, k := range required {
		env = append(env, fmt.Sprintf("%s=%d_%s", k, i, marker))
	}
	env = append(env, "LLMUX_DB_PATH=relative/path/db.sqlite")

	cmd.Env = env
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("expected main to crash with error, but it exited 0")
	}

	output := string(out)
	if strings.Contains(output, marker) {
		t.Errorf("startup output leaked secret marker! Output:\n%s", output)
	}
	if !strings.Contains(output, "LLMUX_DB_PATH") {
		t.Errorf("expected output to mention LLMUX_DB_PATH, got:\n%s", output)
	}
}

func TestMainStartupRejectsInvalidListenAddr(t *testing.T) {
	if os.Getenv("BE_CRASHER") == "1" {
		main()
		return
	}

	marker := "MARKER_SECRET_VALUE_MARKER_PAD32"
	required := []string{
		"LLMUX_PROXY_KEY",
		"LLMUX_ACCOUNT_K1_KEY",
		"LLMUX_ACCOUNT_K2_KEY",
		"LLMUX_ACCOUNT_K3_KEY",
		"LLMUX_AFFINITY_HMAC_KEY",
	}

	tests := []struct {
		name string
		addr string
	}{
		{"Hostname", "localhost:4000"},
		{"Wildcard", "0.0.0.0:4000"},
		{"NonLoopbackLiteral", "8.8.8.8:4000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestMainStartupRejectsInvalidListenAddr")

			env := os.Environ()
			env = append(env, "BE_CRASHER=1")
			for i, k := range required {
				env = append(env, fmt.Sprintf("%s=%d_%s", k, i, marker))
			}
			env = append(env, "LLMUX_DB_PATH=/tmp/db.sqlite")
			env = append(env, "LLMUX_LISTEN_ADDR="+tt.addr)

			cmd.Env = env
			out, err := cmd.CombinedOutput()

			if err == nil {
				t.Fatalf("expected main to reject LLMUX_LISTEN_ADDR=%q and exit non-zero, but it exited 0. Output:\n%s", tt.addr, out)
			}

			output := string(out)
			if !strings.Contains(output, "LLMUX_LISTEN_ADDR") {
				t.Errorf("expected output to mention LLMUX_LISTEN_ADDR, got:\n%s", output)
			}
		})
	}
}
