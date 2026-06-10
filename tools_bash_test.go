//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashDebugLaunch(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}

	adapterPath := os.Getenv("BASH_DEBUG_ADAPTER_PATH")
	if adapterPath == "" {
		adapterPath = "node_modules/vscode-bash-debug/out/bashDebug.js"
	}
	if _, err := os.Stat(adapterPath); err != nil {
		t.Skipf("vscode-bash-debug adapter not found at %s (set BASH_DEBUG_ADAPTER_PATH env var)", adapterPath)
	}

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	scriptPath, _ := filepath.Abs(filepath.Join(ts.cwd, "testdata", "bash", "hello.sh"))
	bpFile, _ := filepath.Abs(filepath.Join(ts.cwd, "testdata", "bash", "hello.sh"))

	_, isErr := ts.callTool(t, "debug", map[string]any{
		"mode":            "source",
		"path":            scriptPath,
		"debugger":        "bash",
		"bashAdapterPath": adapterPath,
		"port":            "0",
		"breakpoints": []any{
			map[string]any{"file": bpFile, "line": 6},
		},
	})
	if isErr {
		t.Fatalf("debug failed: %v", isErr)
	}

	ctxText := ts.getContextContent(t)
	if !strings.Contains(ctxText, "hello.sh") {
		t.Errorf("expected context to contain 'hello.sh', got: %s", ctxText)
	}

	ts.stopDebugger(t)
}
