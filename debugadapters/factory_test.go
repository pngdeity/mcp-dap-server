package debugadapters

import (
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
			got := DefaultDebuggerFor(tc.language)
			if got != tc.want {
				t.Errorf("DefaultDebuggerFor(%q) = %q, want %q", tc.language, got, tc.want)
			}
		})
	}
}
