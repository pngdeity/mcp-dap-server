package debugadapters

import (
	"testing"
)

func testBackendCompliance(t *testing.T, factory func() DebuggerBackend) {
	t.Helper()

	t.Run("TransportMode", func(t *testing.T) {
		mode := factory().TransportMode()
		if mode != "tcp" && mode != "stdio" {
			t.Errorf("expected 'tcp' or 'stdio', got %q", mode)
		}
	})

	t.Run("AdapterID", func(t *testing.T) {
		id := factory().AdapterID()
		if id == "" {
			t.Error("expected non-empty adapter ID")
		}
	})

	t.Run("CoreRequestType", func(t *testing.T) {
		typ := factory().CoreRequestType()
		if typ != "launch" && typ != "attach" {
			t.Errorf("expected 'launch' or 'attach', got %q", typ)
		}
	})

	t.Run("LaunchArgs_binary", func(t *testing.T) {
		args, err := factory().LaunchArgs("binary", "/tmp/test/prog", true, nil)
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("binary mode skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("LaunchArgs_source", func(t *testing.T) {
		args, err := factory().LaunchArgs("source", "/tmp/test/prog", false, nil)
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("source mode skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("CoreArgs", func(t *testing.T) {
		args, err := factory().CoreArgs("/tmp/prog", "/tmp/core")
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("core args skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("AttachArgs", func(t *testing.T) {
		args, err := factory().AttachArgs(12345)
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("attach args skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("RestartArgs", func(t *testing.T) {
		args, err := factory().RestartArgs([]string{"--flag"})
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			return
		}
		if args != nil {
			if _, ok := args["arguments"]; ok {
				t.Log("restart args: launches with custom args")
			}
		}
	})

	t.Run("StdioPipes", func(t *testing.T) {
		r, w := factory().StdioPipes()
		if (r == nil) != (w == nil) {
			t.Error("expected both pipes nil or both non-nil")
		}
		if r != nil {
			t.Log("stdio backend: pipes captured from spawn")
		} else {
			t.Log("tcp backend: no stdio pipes")
		}
	})
}

func TestDelveInterfaceCompliance(t *testing.T) {
	testBackendCompliance(t, func() DebuggerBackend { return &DelveBackend{} })
}

func TestGDBInterfaceCompliance(t *testing.T) {
	testBackendCompliance(t, func() DebuggerBackend {
		return &GDBBackend{GDBPath: "gdb"}
	})
}

func TestBashInterfaceCompliance(t *testing.T) {
	testBackendCompliance(t, func() DebuggerBackend {
		return &BashBackend{
			BashPath:   "/bin/bash",
			CatPath:    "cat",
			MkfifoPath: "mkfifo",
			PkillPath:  "pkill",
		}
	})
}
