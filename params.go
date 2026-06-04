package main

const debugToolDescription = `Start a complete debugging session.

Modes: 'source' (compile & debug), 'binary' (debug executable), 'core' (debug core dump), 'attach' (connect to process).

Debugger selection (via 'debugger' parameter):
- 'delve' (default): For Go programs only. Requires dlv to be installed.
- 'gdb': For C/C++/Rust and other compiled languages. Requires GDB 14+ with native DAP support (gdb -i dap). GDB does not support 'source' mode; compile your program with debug symbols (gcc -g -O0) and use 'binary' mode.
- 'bash': For Bash shell scripts. Requires Node.js and the vscode-bash-debug adapter installed. Set bashAdapterPath to the adapter's out/bashDebug.js. Supports 'source' and 'binary' modes (both launch the script). Does not support 'core' or 'attach' modes.

Choose the debugger based on the language of the program being debugged: use 'delve' for Go, use 'gdb' for C/C++/Rust, use 'bash' for Bash scripts.

By default, when stopped at a breakpoint returns a compact stop summary (location only). Set fullContext: true only if you need variables immediately — leave it false unless you plan to call 'context' right after anyway.`

type BreakpointSpec struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Function string `json:"function,omitempty"`
}

type DebugParams struct {
	Language        string           `json:"language,omitempty" mcp:"language of the program: 'go' (default), 'c'/'cpp', or 'bash' (auto-selects debugger)"`
	Mode            string           `json:"mode" mcp:"'source' (compile & debug), 'binary' (debug executable), 'core' (debug core dump), or 'attach' (connect to process)"`
	Path            string           `json:"path,omitempty" mcp:"program path (required for source/binary modes; optional for core mode with GDB, which can auto-detect it)"`
	Args            []string         `json:"args,omitempty" mcp:"command line arguments for the program"`
	CoreFilePath    string           `json:"coreFilePath,omitempty" mcp:"path to core dump file (required for core mode)"`
	ProcessID       int              `json:"processId,omitempty" mcp:"process ID (required for attach mode)"`
	Breakpoints     []BreakpointSpec `json:"breakpoints,omitempty" mcp:"initial breakpoints"`
	StopOnEntry     bool             `json:"stopOnEntry,omitempty" mcp:"stop at program entry instead of running to first breakpoint"`
	Port            string           `json:"port,omitempty" mcp:"port for DAP server (default: auto-assigned)"`
	Debugger        string           `json:"debugger,omitempty" mcp:"debugger to use: 'delve' (default), 'gdb', or 'bash'. Overrides the language-based default."`
	GDBPath         string           `json:"gdbPath,omitempty" mcp:"path to gdb binary (default: auto-detected from PATH). Requires GDB 14+."`
	BashAdapterPath string           `json:"bashAdapterPath,omitempty" mcp:"path to vscode-bash-debug adapter's out/bashDebug.js (required for bash backend)"`
	BashNodePath    string           `json:"bashNodePath,omitempty" mcp:"path to Node.js binary (default: 'node' from PATH)"`
	BashBashPath    string           `json:"bashBashPath,omitempty" mcp:"path to bash binary (default: '/bin/bash')"`
	BashCatPath     string           `json:"bashCatPath,omitempty" mcp:"path to cat binary (default: 'cat')"`
	BashMkfifoPath  string           `json:"bashMkfifoPath,omitempty" mcp:"path to mkfifo binary (default: 'mkfifo')"`
	BashPkillPath   string           `json:"bashPkillPath,omitempty" mcp:"path to pkill binary (default: 'pkill')"`
	ProtocolLog     string           `json:"protocolLog,omitempty" mcp:"file path for protocol-level DAP message logging (what the MCP server sends/receives)"`
	ToolLog         string           `json:"toolLog,omitempty" mcp:"file path for tool-level DAP logging (native debugger logging, GDB only)"`
	FullContext     bool             `json:"fullContext,omitempty" mcp:"if true, return full context (stack trace and variables) when stopped at a breakpoint; if false (default), return a compact stop summary — leave false unless you need variables immediately"`
}

type ContextParams struct {
	ThreadID  FlexInt `json:"threadId,omitempty" mcp:"thread to inspect (default: current thread)"`
	FrameID   FlexInt `json:"frameId,omitempty" mcp:"frame to focus on (default: top frame)"`
	MaxFrames FlexInt `json:"maxFrames,omitempty" mcp:"maximum stack frames (default: 20)"`
}

type StepParams struct {
	Mode        string  `json:"mode" mcp:"'over' (next line), 'in' (into function), 'out' (out of function)"`
	ThreadID    FlexInt `json:"threadId,omitempty" mcp:"thread to step (default: current thread)"`
	FullContext bool    `json:"fullContext,omitempty" mcp:"if true, return full context (stack trace and variables) when stopped; if false (default), return a compact stop summary — leave false unless you need variables immediately"`
}

type InfoParams struct {
	Type string `json:"type,omitempty" mcp:"'threads' (list threads), 'sources' (loaded source files), 'modules' (loaded modules), or 'registers' (CPU register values at current frame, GDB only)"`
}

type BreakpointToolParams struct {
	File     string  `json:"file,omitempty" mcp:"source file path (required if no function)"`
	Line     FlexInt `json:"line,omitempty" mcp:"line number (required if file provided)"`
	Function string  `json:"function,omitempty" mcp:"function name (alternative to file+line)"`
}

type ClearBreakpointsParams struct {
	File string `json:"file,omitempty" mcp:"clear all breakpoints in this file"`
	All  bool   `json:"all,omitempty" mcp:"clear all breakpoints"`
}

type StopParams struct {
	Detach bool `json:"detach,omitempty" mcp:"if true, detach from the process without terminating it (leaves the debuggee running); default false terminates the debuggee"`
}

type ContinueParams struct {
	ThreadID    FlexInt         `json:"threadId,omitempty" mcp:"thread to continue (default: all threads)"`
	To          *BreakpointSpec `json:"to,omitempty" mcp:"location to run to (sets temporary breakpoint)"`
	FullContext bool            `json:"fullContext,omitempty" mcp:"if true, return full context (stack trace and variables) when stopped; if false (default), return a compact stop summary — leave false unless you need variables immediately"`
}

type PauseParams struct {
	ThreadID FlexInt `json:"threadId" mcp:"thread ID to pause"`
}

type EvaluateParams struct {
	Expression string   `json:"expression" mcp:"expression to evaluate"`
	FrameID    *FlexInt `json:"frameId,omitempty" mcp:"stack frame ID for evaluation context (default: current frame)"`
	Context    string   `json:"context,omitempty" mcp:"context for evaluation: watch, repl, hover (default: watch)"`
}

type SetVariableParams struct {
	VariablesReference FlexInt `json:"variablesReference" mcp:"reference to the variable container"`
	Name               string  `json:"name" mcp:"name of the variable to set"`
	Value              string  `json:"value" mcp:"new value for the variable"`
}

type RestartParams struct {
	Args []string `json:"args,omitempty" mcp:"new command line arguments for the program upon restart, or empty to reuse previous arguments"`
}

type DisassembleParams struct {
	Address string  `json:"address" mcp:"memory address to disassemble (e.g. '0x00400780')"`
	Offset  FlexInt `json:"offset,omitempty" mcp:"instruction offset from address (default: 0)"`
	Count   FlexInt `json:"count,omitempty" mcp:"number of instructions to disassemble (default: 20)"`
}
