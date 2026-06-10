//go:build integration

package debugadapters

import (
	"io"
	"os/exec"
	"testing"
)

func TestBashBackendSpawn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}

	adapterPath := "node_modules/vscode-bash-debug/out/bashDebug.js"
	if _, err := exec.LookPath(adapterPath); err != nil {
		t.Skip("vscode-bash-debug adapter not found")
	}

	backend := &BashBackend{AdapterPath: adapterPath}
	cmd, listenAddr, err := backend.Spawn(":0", io.Discard)
	if err != nil {
		t.Fatalf("failed to spawn bash-debug-adapter: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	if listenAddr != "" {
		t.Errorf("expected empty listen address for stdio transport, got: %s", listenAddr)
	}
	if backend.TransportMode() != "stdio" {
		t.Errorf("expected stdio transport, got: %s", backend.TransportMode())
	}
}
