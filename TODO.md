# TODO: Language-to-Debugger Server Configuration

## Goal

Allow users to specify which debugger backend is used for a given language,
with graceful fallback to hardcoded defaults when no configuration is present.

## Precedence Chain (highest to lowest)

```
Tool param `debugger`  →  Env var `MCPDAP_DEBUGGER_<LANG>`  →  hardcoded `defaultDebuggerFor()`
```

Path defaults follow a parallel chain:

```
Tool param `<Backend>Path`  →  Env var `MCPDAP_<BACKEND>_PATH`  →  `exec.LookPath(...)`
```

## Configuration Mechanism

Env vars only. Zero new Go dependencies. Matches MCP ecosystem convention and
maps directly to OpenCode's `"environment"` field in `opencode.json`.

### OpenCode Config Example

```json
{
  "mcp": {
    "dap": {
      "type": "local",
      "command": ["/path/to/bin/mcp-dap-server"],
      "environment": {
        "MCPDAP_DEBUGGER_C": "lldb",
        "MCPDAP_DEBUGGER_CPP": "lldb",
        "MCPDAP_GDB_PATH": "/opt/gdb/bin/gdb",
        "MCPDAP_BASH_ADAPTER_PATH": "/home/user/vscode-bash-debug/out/bashDebug.js"
      }
    }
  }
}
```

### Env Var Naming Convention

```
MCPDAP_DEBUGGER_<LANGUAGE>     →  override debugger for a language
MCPDAP_GDB_PATH               →  path to GDB binary
MCPDAP_BASH_ADAPTER_PATH       →  path to bash-debug-adapter's bashDebug.js
MCPDAP_BASH_NODE_PATH          →  path to Node.js binary
```

Language keys are lowercased env var name suffixes (e.g. `MCPDAP_DEBUGGER_RUST` →
language `rust`).

## Files to Create or Change

### 1. New file: `config.go` (~40 lines)

Load env vars into a `ServerConfig` struct. **Do not scan `os.Environ()`** —
read each known key individually with `os.Getenv`. This avoids silently picking
up typos (`MCPDAP_DEBUGGER_CPPP` would be ingested as language `cppp`).

```go
package main

import "os"

type ServerConfig struct {
    LanguageDebuggers map[string]string
    GDBPath           string
    BashAdapterPath   string
    BashNodePath      string
}

func loadServerConfig() *ServerConfig {
    cfg := &ServerConfig{
        LanguageDebuggers: make(map[string]string),
    }
    for _, lang := range []string{"go", "c", "cpp", "bash", "rust"} {
        if v := os.Getenv("MCPDAP_DEBUGGER_" + strings.ToUpper(lang)); v != "" {
            cfg.LanguageDebuggers[lang] = v
        }
    }
    cfg.GDBPath         = os.Getenv("MCPDAP_GDB_PATH")
    cfg.BashAdapterPath = os.Getenv("MCPDAP_BASH_ADAPTER_PATH")
    cfg.BashNodePath    = os.Getenv("MCPDAP_BASH_NODE_PATH")
    return cfg
}
```

**Open question:** Should the supported language list be hardcoded as above, or
generated from the env vars at runtime? Hardcoded is safer (no silent typos),
but limits extensibility to languages known at compile time. If the debugger
backend set is extensible (new backends added via config), then a runtime scan
is correct — but that should come with validation that the resolved debugger
name maps to a known backend.

### 2. Modifications to `backend.go`

#### `defaultDebuggerFor` gains a `cfg` parameter

```go
func defaultDebuggerFor(language string, cfg *ServerConfig) string {
    if d, ok := cfg.LanguageDebuggers[language]; ok && d != "" {
        return d
    }
    switch language {
    case "go":       return "delve"
    case "c","cpp":  return "gdb"
    case "bash","sh":return "bash"
    }
    return ""
}
```

#### `newBackend` receives resolved values, not raw config

**Critical: Pre-resolve debugger and paths before calling `newBackend`.**
Do not pass `*ServerConfig` into `newBackend`. Extract resolution functions
that produce final values, then pass those to the factory.

```go
func resolveDebugger(params DebugParams, cfg *ServerConfig) string {
    if params.Debugger != "" {
        return params.Debugger
    }
    if params.Language != "" {
        return defaultDebuggerFor(params.Language, cfg)
    }
    return "delve"
}

func resolvePaths(params DebugParams, cfg *ServerConfig) (gdbPath, bashAdapter, bashNode string) {
    gdbPath = params.GDBPath
    if gdbPath == "" { gdbPath = cfg.GDBPath }
    if gdbPath == "" { gdbPath, _ = exec.LookPath("gdb") }

    bashAdapter = params.BashAdapterPath
    if bashAdapter == "" { bashAdapter = cfg.BashAdapterPath }

    bashNode = params.BashNodePath
    if bashNode == "" { bashNode = cfg.BashNodePath }

    return
}
```

#### `newBackend` takes resolved values directly

```go
func newBackend(debugger, gdbPath, toolLogPath, bashAdapter, bashNode string,
    bashPaths BashPaths, programArgs []string) (DebuggerBackend, error) {
    switch debugger {
    case "delve":
        return &delveBackend{}, nil
    case "gdb":
        if gdbPath == "" {
            return nil, fmt.Errorf("GDB not found; install GDB 14+ or set gdbPath / MCPDAP_GDB_PATH")
        }
        return &gdbBackend{gdbPath: gdbPath, toolLogPath: toolLogPath}, nil
    case "bash":
        if bashAdapter == "" {
            return nil, fmt.Errorf("bashAdapterPath is required; set the parameter or MCPDAP_BASH_ADAPTER_PATH")
        }
        return &bashdbBackend{...}, nil
    default:
        return nil, fmt.Errorf("unsupported debugger: %s", debugger)
    }
}
```

This keeps `newBackend` ignorant of config precedence. The merging logic lives
in `resolveDebugger` and `resolvePaths`, both testable in isolation. Future
backend authors don't need to understand the config layer.

### 3. Modifications to `tools.go`

In the `debug()` handler, replace the `newBackend(params)` call with:

```go
resolvedDebugger := resolveDebugger(params, ds.cfg)
resolvedGDB, resolvedBashAdapter, resolvedBashNode := resolvePaths(params, ds.cfg)
backend, err := newBackend(resolvedDebugger, resolvedGDB, params.ToolLog,
    resolvedBashAdapter, resolvedBashNode, bashPathsFromParams(params), params.Args)
```

This requires `debuggerSession` to hold a `cfg *ServerConfig` field, set during
`registerTools`.

### 4. Modifications to `main.go`

```go
cfg := loadServerConfig()
ds := registerTools(server, logWriter, cfg)
```

`registerTools` stores `cfg` on the `debuggerSession`.

## Integration Threading

```
main.go                     session.go              tools.go          backend.go
────────                    ──────────              ────────          ──────────
loadServerConfig() ──→ registerTools(cfg)
                            ds.cfg = cfg
                                                     debug():
                                                       resolveDebugger()
                                                       resolvePaths()
                                                       newBackend()
                                                       defaultDebuggerFor()
```

## Graceful Failure Behavior

| Scenario | Outcome |
|----------|---------|
| No env vars set | Hardcoded `defaultDebuggerFor()` mapping used (current behavior, zero regression) |
| `MCPDAP_DEBUGGER_C=lldb` but no lldb backend | `newBackend` returns `"unsupported debugger: lldb"` — caught at `debug` invocation |
| `MCPDAP_GDB_PATH=/nonexistent` | `debug()` spawn fails — error from `exec.Command` surfaces to the MCP tool result |
| `MCPDAP_DEBUGGER_GO=delve` (redundant) | Works identically, just bypasses the hardcoded switch |
| `MCPDAP_DEBUGGER_C=` (empty value) | Env returns `""`, `defaultDebuggerFor` treats empty as unset, falls through to `gdb` |

## What This Doesn't Do (Intentionally)

| Not doing | Why |
|-----------|-----|
| Config file (YAML/JSON/TOML) | Overkill for <10 values. Env vars are the MCP ecosystem standard. Zero new Go deps. |
| Dynamic reloading | Server is short-lived (one MCP session). Config loaded once at startup. |
| Binary validation at startup | Debuggers may not be on PATH until a debugee is selected. Validation happens at invocation. |
| Per-tool config | One server = one config. Different mappings require separate server instances. |

## Design Properties

- **Loose coupling**: `newBackend` receives resolved values, not raw config. Config merging is isolated in resolution functions. Backend constructors don't need to know about env vars.
- **Extensible**: New language→debugger mappings work via env vars without code changes. New backends require a case in `newBackend`'s switch, which is unavoidable — the backend struct needs to exist.
- **Zero regression**: No env vars → identical behavior to current code. The hardcoded switch in `defaultDebuggerFor` is the bottom of the precedence chain, never removed.
- **Testable**: `resolveDebugger` and `resolvePaths` are pure functions that take structs and return values. No env var access, no I/O.

## Verification

- [ ] `go build ./...` succeeds with no new dependencies in `go.mod`
- [ ] Default path (no env vars): identical behavior to current
- [ ] `MCPDAP_DEBUGGER_C=lldb` returns error "unsupported debugger: lldb" (no lldb backend)
- [ ] `MCPDAP_DEBUGGER_GO=gdb` → go source mode fails (gdb rejects source mode)
- [ ] `MCPDAP_GDB_PATH` overrides default gdb lookup
- [ ] Tool-param `"debugger": "delve"` overrides `MCPDAP_DEBUGGER_GO=gdb`
- [ ] Empty env var values fall through to defaults
- [ ] All existing tests pass unchanged (no env vars set → current behavior)

## Related

- `ARCHITECTURE_REVIEW.md` — full architecture assessment
- `PROTOCOL_COVERAGE.md` — DAP and MCP protocol coverage analysis
