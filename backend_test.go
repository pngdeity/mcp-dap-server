package main

import (
	"strings"
	"testing"

	"github.com/pngdeity/mcp-dap-server/debugadapters"
)

func TestNewBackend(t *testing.T) {
	t.Run("delve", func(t *testing.T) {
		b, err := newBackend(DebugParams{Debugger: "delve"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := b.(*debugadapters.DelveBackend); !ok {
			t.Errorf("expected *debugadapters.DelveBackend, got %T", b)
		}
	})

	t.Run("gdb", func(t *testing.T) {
		b, err := newBackend(DebugParams{Debugger: "gdb"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gb, ok := b.(*debugadapters.GDBBackend)
		if !ok {
			t.Fatalf("expected *debugadapters.GDBBackend, got %T", b)
		}
		if gb.GDBPath == "" {
			t.Error("expected GDBPath to be resolved")
		}
	})

	t.Run("bash", func(t *testing.T) {
		b, err := newBackend(DebugParams{
			Debugger:        "bash",
			BashAdapterPath: "/path/to/adapter.js",
			BashBashPath:    "/bin/bash",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		bb, ok := b.(*debugadapters.BashBackend)
		if !ok {
			t.Fatalf("expected *debugadapters.BashBackend, got %T", b)
		}
		if bb.AdapterPath != "/path/to/adapter.js" {
			t.Errorf("expected AdapterPath to be preserved, got %q", bb.AdapterPath)
		}
		if bb.BashPath != "/bin/bash" {
			t.Errorf("expected BashPath to be preserved, got %q", bb.BashPath)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		_, err := newBackend(DebugParams{Debugger: "nonexistent"})
		if err == nil {
			t.Fatal("expected error for unsupported debugger")
		}
		if !strings.Contains(err.Error(), "unsupported debugger") {
			t.Errorf("expected error to mention 'unsupported debugger', got: %s", err.Error())
		}
	})
}

func TestNewBackendLanguageFallback(t *testing.T) {
	tests := []struct {
		name     string
		params   DebugParams
		wantType string
	}{
		{
			name:     "go language",
			params:   DebugParams{Language: "go"},
			wantType: "delve",
		},
		{
			name:     "c language",
			params:   DebugParams{Language: "c"},
			wantType: "gdb",
		},
		{
			name:     "bash language",
			params:   DebugParams{Language: "bash"},
			wantType: "bashdb",
		},
		{
			name:     "sh language",
			params:   DebugParams{Language: "sh"},
			wantType: "bashdb",
		},
		{
			name:     "unknown language falls back to delve",
			params:   DebugParams{Language: "python"},
			wantType: "delve",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := newBackend(tc.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch tc.wantType {
			case "delve":
				if _, ok := b.(*debugadapters.DelveBackend); !ok {
					t.Errorf("expected *debugadapters.DelveBackend, got %T", b)
				}
			case "gdb":
				if _, ok := b.(*debugadapters.GDBBackend); !ok {
					t.Errorf("expected *debugadapters.GDBBackend, got %T", b)
				}
			case "bashdb":
				if _, ok := b.(*debugadapters.BashBackend); !ok {
					t.Errorf("expected *debugadapters.BashBackend, got %T", b)
				}
			}
		})
	}
}
