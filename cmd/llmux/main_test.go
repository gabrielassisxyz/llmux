package main

import (
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

	marker := "MARKER_SECRET_VALUE_MARKER"
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
	for _, k := range required {
		env = append(env, k+"="+marker)
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
