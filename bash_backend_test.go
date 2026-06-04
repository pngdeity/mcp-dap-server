package main

import (
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestBashBackendLaunchArgs(t *testing.T) {
	backend := &bashdbBackend{
		bashPath:   "/bin/bash",
		catPath:    "cat",
		mkfifoPath: "mkfifo",
		pkillPath:  "pkill",
	}

	t.Run("source mode", func(t *testing.T) {
		args, err := backend.LaunchArgs("source", "/path/to/script.sh", true, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if args["type"] != "bashdb" {
			t.Errorf("expected type 'bashdb', got: %v", args["type"])
		}
		if args["program"] != "/path/to/script.sh" {
			t.Errorf("expected program '/path/to/script.sh', got: %v", args["program"])
		}
		if args["pathBash"] != "/bin/bash" {
			t.Errorf("expected pathBash '/bin/bash', got: %v", args["pathBash"])
		}
		if args["pathCat"] != "cat" {
			t.Errorf("expected pathCat 'cat', got: %v", args["pathCat"])
		}
		if args["pathMkfifo"] != "mkfifo" {
			t.Errorf("expected pathMkfifo 'mkfifo', got: %v", args["pathMkfifo"])
		}
		if args["pathPkill"] != "pkill" {
			t.Errorf("expected pathPkill 'pkill', got: %v", args["pathPkill"])
		}
		if args["terminalKind"] != "debugConsole" {
			t.Errorf("expected terminalKind 'debugConsole', got: %v", args["terminalKind"])
		}
		if args["stopOnEntry"] != true {
			t.Errorf("expected stopOnEntry true, got: %v", args["stopOnEntry"])
		}
		if _, ok := args["args"]; ok {
			t.Error("expected no args key when programArgs is nil")
		}
	})

	t.Run("binary mode", func(t *testing.T) {
		args, err := backend.LaunchArgs("binary", "/path/to/script.sh", false, []string{"--flag", "value"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if args["type"] != "bashdb" {
			t.Errorf("expected type 'bashdb', got: %v", args["type"])
		}
		if args["program"] != "/path/to/script.sh" {
			t.Errorf("expected program '/path/to/script.sh', got: %v", args["program"])
		}
		if _, ok := args["stopOnEntry"]; ok {
			t.Error("expected no stopOnEntry key when false")
		}
		programArgs, ok := args["args"].([]string)
		if !ok {
			t.Fatalf("expected args to be []string, got: %T", args["args"])
		}
		if len(programArgs) != 2 || programArgs[0] != "--flag" || programArgs[1] != "value" {
			t.Errorf("unexpected args: %v", programArgs)
		}
	})

	t.Run("unsupported mode", func(t *testing.T) {
		_, err := backend.LaunchArgs("attach", "/path/to/script.sh", false, nil)
		if err == nil {
			t.Error("expected error for unsupported mode")
		}
	})
}

func TestBashBackendCoreArgsError(t *testing.T) {
	backend := &bashdbBackend{}
	_, err := backend.CoreArgs("/path/to/program", "/path/to/core")
	if err == nil {
		t.Fatal("expected error for core mode")
	}
	if !strings.Contains(err.Error(), "core dump") {
		t.Errorf("expected error message to mention 'core dump', got: %s", err.Error())
	}
}

func TestBashBackendAttachArgsError(t *testing.T) {
	backend := &bashdbBackend{}
	_, err := backend.AttachArgs(12345)
	if err == nil {
		t.Fatal("expected error for attach mode")
	}
	if !strings.Contains(err.Error(), "attach") {
		t.Errorf("expected error message to mention 'attach', got: %s", err.Error())
	}
}

func TestBashBackendAdapterID(t *testing.T) {
	backend := &bashdbBackend{}
	if backend.AdapterID() != "bashdb" {
		t.Errorf("expected 'bashdb', got: %s", backend.AdapterID())
	}
}

func TestBashBackendTransportMode(t *testing.T) {
	backend := &bashdbBackend{}
	if backend.TransportMode() != "stdio" {
		t.Errorf("expected 'stdio', got: %s", backend.TransportMode())
	}
}

func TestBashBackendSpawn(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}

	adapterPath := "node_modules/vscode-bash-debug/out/bashDebug.js"
	if _, err := exec.LookPath(adapterPath); err != nil {
		t.Skip("vscode-bash-debug adapter not found")
	}

	backend := &bashdbBackend{adapterPath: adapterPath}
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
