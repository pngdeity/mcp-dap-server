package main

import (
	"strings"
	"testing"
)

func TestDefaultDebuggerFor(t *testing.T) {
	tests := []struct {
		language string
		want     string
	}{
		{"go", "delve"},
		{"c", "gdb"},
		{"cpp", "gdb"},
		{"c++", "gdb"},
		{"bash", "bash"},
		{"sh", "bash"},
		{"python", ""},
		{"rust", ""},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			got := defaultDebuggerFor(tc.language)
			if got != tc.want {
				t.Errorf("defaultDebuggerFor(%q) = %q, want %q", tc.language, got, tc.want)
			}
		})
	}
}

func TestNewBackend(t *testing.T) {
	t.Run("delve", func(t *testing.T) {
		b, err := newBackend(DebugParams{Debugger: "delve"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := b.(*delveBackend); !ok {
			t.Errorf("expected *delveBackend, got %T", b)
		}
	})

	t.Run("gdb", func(t *testing.T) {
		b, err := newBackend(DebugParams{Debugger: "gdb"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gb, ok := b.(*gdbBackend)
		if !ok {
			t.Fatalf("expected *gdbBackend, got %T", b)
		}
		if gb.gdbPath == "" {
			t.Error("expected gdbPath to be resolved")
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
		bb, ok := b.(*bashdbBackend)
		if !ok {
			t.Fatalf("expected *bashdbBackend, got %T", b)
		}
		if bb.adapterPath != "/path/to/adapter.js" {
			t.Errorf("expected adapterPath to be preserved, got %q", bb.adapterPath)
		}
		if bb.bashPath != "/bin/bash" {
			t.Errorf("expected bashPath to be preserved, got %q", bb.bashPath)
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
				if _, ok := b.(*delveBackend); !ok {
					t.Errorf("expected *delveBackend, got %T", b)
				}
			case "gdb":
				if _, ok := b.(*gdbBackend); !ok {
					t.Errorf("expected *gdbBackend, got %T", b)
				}
			case "bashdb":
				if _, ok := b.(*bashdbBackend); !ok {
					t.Errorf("expected *bashdbBackend, got %T", b)
				}
			}
		})
	}
}
