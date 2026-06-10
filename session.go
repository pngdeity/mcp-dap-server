package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/google/go-dap"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pngdeity/mcp-dap-server/debugadapters"
)

type debuggerSession struct {
	mu              sync.Mutex
	cmd             *exec.Cmd
	client          *DAPClient
	server          *mcp.Server
	logWriter       io.Writer
	backend         debugadapters.DebuggerBackend
	capabilities    dap.Capabilities
	launchMode      string
	programPath     string
	programArgs     []string
	coreFilePath    string
	stoppedThreadID int
	lastFrameID     int
	protocolLogFile *os.File
}

func (ds *debuggerSession) defaultThreadID() int {
	if ds.stoppedThreadID != 0 {
		return ds.stoppedThreadID
	}
	return 1
}

func registerTools(server *mcp.Server, logWriter io.Writer) *debuggerSession {
	ds := &debuggerSession{server: server, logWriter: logWriter, lastFrameID: -1}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "debug",
		Description: debugToolDescription,
	}, ds.debug)

	return ds
}

func (ds *debuggerSession) sessionToolNames() []string {
	tools := []string{
		"stop",
		"breakpoint",
		"clear-breakpoints",
		"continue",
		"step",
		"pause",
		"context",
		"evaluate",
		"info",
	}

	if ds.capabilities.SupportsRestartRequest {
		tools = append(tools, "restart")
	}
	if ds.capabilities.SupportsSetVariable {
		tools = append(tools, "set-variable")
	}
	if ds.capabilities.SupportsDisassembleRequest {
		tools = append(tools, "disassemble")
	}

	return tools
}

func (ds *debuggerSession) registerSessionTools() {
	ds.server.RemoveTools("debug")

	mcp.AddTool(ds.server, &mcp.Tool{
		Name:        "stop",
		Description: "End the debugging session. By default terminates the debuggee. Pass detach=true to detach without killing the process (leaves it running); detach requires adapter support.",
	}, ds.stop)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name: "breakpoint",
		Description: `Set a breakpoint. Provide EITHER file+line OR function name (not both).

Examples: {"file": "/path/to/main.go", "line": 42} or {"function": "main.processData"}`,
	}, ds.breakpoint)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name: "clear-breakpoints",
		Description: `Remove breakpoints. Provide 'file' to clear breakpoints in a specific file, or 'all': true to clear all breakpoints.

Examples: {"file": "/path/to/main.go"} or {"all": true}`,
	}, ds.clearBreakpoints)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name: "continue",
		Description: `Continue program execution until the next breakpoint or termination.

By default returns a compact stop summary (location only). Set fullContext: true only if you need variables immediately — it saves a separate 'context' call but returns much more data. Leave fullContext false (the default) unless you know you need variables right away.

Optionally specify 'to' for run-to-cursor: {"to": {"file": "/path/main.go", "line": 50}} or {"to": {"function": "main.Run"}}`,
	}, ds.continueExecution)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name: "step",
		Description: `Step through code one line at a time.

By default returns a compact stop summary (location only). Set fullContext: true only if you need variables immediately — it saves a separate 'context' call but returns much more data. Leave fullContext false (the default) unless you know you need variables right away.

Modes: 'over' (execute current line, step over function calls), 'in' (step into function calls), 'out' (run until current function returns).`,
	}, ds.step)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name:        "pause",
		Description: "Pause a running program. Use 'context' afterwards to inspect the current state.",
	}, ds.pauseExecution)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name: "context",
		Description: `Get full debugging context at the current stop location. Always returns ALL of the following — source location, full stack trace, and all variables with types and values. There are no flags to control what is included; everything is always returned.

Call with {} (no arguments) to use the current thread and top frame. Only three optional parameters exist: threadId, frameId, maxFrames. Do NOT pass any other parameters. Use 'info' with type 'threads' to discover valid thread IDs.`,
	}, ds.context)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name: "evaluate",
		Description: `Evaluate an expression in the debugged program's context. Returns the result value and type. All parameters except 'expression' are optional.

The default context is 'watch', which evaluates language expressions (C, C++, Go). Use valid language syntax, not debugger commands.

Examples: {"expression": "x + y"}, {"expression": "*ptr"}, {"expression": "$rsp"}, {"expression": "(int)value"}

For GDB commands (e.g. print/x), use context 'repl': {"expression": "print/x var", "context": "repl"}`,
	}, ds.evaluateExpression)

	infoTypes := "'threads' (list all threads with IDs, default)"
	if ds.capabilities.SupportsLoadedSourcesRequest {
		infoTypes += ", 'sources' (loaded source file paths)"
	}
	if ds.capabilities.SupportsModulesRequest {
		infoTypes += ", 'modules' (loaded modules/libraries)"
	}
	infoTypes += ", 'registers' (CPU register values at current frame, GDB only)"
	infoDesc := fmt.Sprintf("List program metadata. Type: %s.", infoTypes)
	mcp.AddTool(ds.server, &mcp.Tool{
		Name:        "info",
		Description: infoDesc,
	}, ds.info)

	if ds.capabilities.SupportsRestartRequest {
		mcp.AddTool(ds.server, &mcp.Tool{
			Name:        "restart",
			Description: "Restart the debugging session from the beginning. Optionally provide new command line arguments via 'args', or omit to reuse the previous arguments.",
		}, ds.restartDebugger)
	}
	if ds.capabilities.SupportsSetVariable {
		mcp.AddTool(ds.server, &mcp.Tool{
			Name: "set-variable",
			Description: `Modify a variable's value in the debugged program. Requires the variablesReference from a previous 'context' call's scope.

Example: {"variablesReference": 1000, "name": "count", "value": "42"}`,
		}, ds.setVariable)
	}
	if ds.capabilities.SupportsDisassembleRequest {
		mcp.AddTool(ds.server, &mcp.Tool{
			Name: "disassemble",
			Description: `Disassemble machine code at a memory address. Returns assembly instructions.

Example: {"address": "0x00400780"} or {"address": "0x00400780", "count": 30}
The 'address' is a hex memory address (e.g. from instructionPointerReference in a stack frame). 'count' defaults to 20 instructions.`,
		}, ds.disassembleCode)
	}
}

func (ds *debuggerSession) unregisterSessionTools() {
	ds.server.RemoveTools(ds.sessionToolNames()...)

	mcp.AddTool(ds.server, &mcp.Tool{
		Name:        "debug",
		Description: debugToolDescription,
	}, ds.debug)
}

func (ds *debuggerSession) cleanup() {
	if ds.client != nil {
		ds.client.Close()
		ds.client = nil
	}
	if ds.protocolLogFile != nil {
		ds.protocolLogFile.Close()
		ds.protocolLogFile = nil
	}

	if ds.cmd != nil && ds.cmd.Process != nil {
		if err := ds.cmd.Process.Kill(); err != nil {
			if !strings.Contains(err.Error(), "process already finished") {
				log.Printf("cleanup: error killing debugger process: %v", err)
			}
		}
		ds.cmd.Wait()
		ds.cmd = nil
	}

	ds.launchMode = ""
	ds.programPath = ""
	ds.programArgs = nil
	ds.coreFilePath = ""
	ds.capabilities = dap.Capabilities{}
	ds.stoppedThreadID = 0
	ds.lastFrameID = -1
	ds.unregisterSessionTools()
}

func (ds *debuggerSession) getThreadList() string {
	if ds.client == nil {
		return ""
	}
	seq, err := ds.client.ThreadsRequest()
	if err != nil {
		log.Printf("getThreadList: ThreadsRequest failed: %v", err)
		return ""
	}
	resp, err := readTypedResponse[*dap.ThreadsResponse](ds.client, seq)
	if err != nil {
		log.Printf("getThreadList: readTypedResponse failed: %v", err)
		return ""
	}
	var threads strings.Builder
	for _, t := range resp.Body.Threads {
		fmt.Fprintf(&threads, "  Thread %d: %s\n", t.Id, t.Name)
	}
	return threads.String()
}

func (ds *debuggerSession) getFullContext(threadID, frameID, maxFrames int) (*mcp.CallToolResult, error) {
	if ds.client == nil {
		return nil, fmt.Errorf("debugger not started")
	}

	var result strings.Builder

	stSeq, err := ds.client.StackTraceRequest(threadID, 0, maxFrames)
	if err != nil {
		return nil, err
	}
	stResp, err := readTypedResponse[*dap.StackTraceResponse](ds.client, stSeq)
	if err != nil {
		return nil, fmt.Errorf("unable to get stack trace: %w", err)
	}
	frames := stResp.Body.StackFrames

	if len(frames) > 0 {
		top := frames[0]
		result.WriteString("## Current Location\n")
		fmt.Fprintf(&result, "Function: %s\n", top.Name)
		if top.Source != nil {
			fmt.Fprintf(&result, "File: %s:%d\n", top.Source.Path, top.Line)
		}
		result.WriteString("\n")
	}

	result.WriteString("## Stack Trace\n")
	for i, frame := range frames {
		fmt.Fprintf(&result, "#%d (Frame ID: %d) %s", i, frame.Id, frame.Name)
		if frame.Source != nil && frame.Source.Path != "" {
			fmt.Fprintf(&result, " at %s:%d", frame.Source.Path, frame.Line)
		}
		if frame.InstructionPointerReference != "" {
			fmt.Fprintf(&result, " [ip: %s]", frame.InstructionPointerReference)
		}
		if frame.PresentationHint == "subtle" {
			result.WriteString(" (runtime)")
		}
		result.WriteString("\n")
	}
	result.WriteString("\n")

	targetFrameID := frameID
	if targetFrameID == 0 && len(frames) > 0 {
		targetFrameID = frames[0].Id
	}
	ds.lastFrameID = targetFrameID

	ds.writeScopesAndVariables(&result, targetFrameID)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: result.String()}},
	}, nil
}

func stopSummary(full *mcp.CallToolResult, reason string) *mcp.CallToolResult {
	text := ""
	if len(full.Content) > 0 {
		if tc, ok := full.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	var summary strings.Builder
	if reason != "" {
		fmt.Fprintf(&summary, "Stopped: %s\n", reason)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Function:") || strings.HasPrefix(line, "File:") {
			summary.WriteString(line + "\n")
		}
	}
	summary.WriteString("Call 'context' to inspect stack trace and variables.")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary.String()}},
	}
}

func (ds *debuggerSession) writeScopesAndVariables(result *strings.Builder, frameID int) {
	scopesSeq, err := ds.client.ScopesRequest(frameID)
	if err != nil {
		result.WriteString("## Variables\n(unable to retrieve scopes)\n")
		return
	}

	scopesResp, err := readTypedResponse[*dap.ScopesResponse](ds.client, scopesSeq)
	if err != nil {
		result.WriteString("## Variables\n(unable to retrieve scopes)\n")
		return
	}

	scopes := scopesResp.Body.Scopes
	if len(scopes) == 0 {
		return
	}

	result.WriteString("## Variables\n")
	for _, scope := range scopes {
		if scope.Name == "Registers" {
			continue
		}
		fmt.Fprintf(result, "### %s\n", scope.Name)
		if scope.VariablesReference <= 0 {
			continue
		}
		varSeq, err := ds.client.VariablesRequest(scope.VariablesReference)
		if err != nil {
			result.WriteString("  (unable to retrieve variables)\n")
			continue
		}
		varResp, err := readTypedResponse[*dap.VariablesResponse](ds.client, varSeq)
		if err != nil {
			result.WriteString("  (unable to retrieve variables)\n")
			continue
		}
		for _, v := range varResp.Body.Variables {
			if v.Type != "" {
				fmt.Fprintf(result, "  %s (%s) = %s\n", v.Name, v.Type, v.Value)
			} else {
				fmt.Fprintf(result, "  %s = %s\n", v.Name, v.Value)
			}
		}
	}
}
