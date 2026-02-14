# go-cmdexec

A Go library for executing external commands with support for timeouts, retries, concurrent execution, signal handling, command policies, output limits, streaming hooks, and testing via a mock executor.

## Installation

```bash
go get github.com/jaeyeom/go-cmdexec
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jaeyeom/go-cmdexec"
)

func main() {
	ctx := context.Background()
	executor := cmdexec.NewBasicExecutor()

	// Simple command execution
	output, err := cmdexec.Output(ctx, executor, "echo", "hello world")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(output))
}
```

## Features

### Basic Execution

`BasicExecutor` runs external commands and captures stdout, stderr, exit code, and timing information.

```go
executor := cmdexec.NewBasicExecutor()
result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command:    "ls",
	Args:       []string{"-la"},
	WorkingDir: "/tmp",
})
fmt.Println(result.Output)
fmt.Println(result.ExitCode)
fmt.Println(result.Duration())
```

### Timeouts and Retries

```go
result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command:    "curl",
	Args:       []string{"https://example.com"},
	Timeout:    5 * time.Second,
	MaxRetries: 3,
	RetryDelay: time.Second,
})
```

### Environment Variables and Stdin

```go
result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command: "grep",
	Args:    []string{"pattern"},
	Env:     map[string]string{"LC_ALL": "C"},
	Stdin:   strings.NewReader("search in this text\npattern found here\n"),
})
```

For retries with stdin, use `StdinFactory` to provide fresh input on each attempt:

```go
result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command:    "process-data",
	MaxRetries: 3,
	StdinFactory: func() io.Reader {
		return strings.NewReader("fresh data\n")
	},
})
// Note: Stdin + MaxRetries without StdinFactory is rejected at validation time
```

### Command Builders

Control how commands are invoked with `CommandBuilder`:

- **`DirectCommandBuilder`** (default) — executes the command directly via `exec.Command`. Preferred for security and reliability.
- **`ShellCommandBuilder`** — wraps the command in `sh -c` with proper quoting. Useful for tools that work better in a shell environment (e.g., Bazel, Gradle).

```go
result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command:        "bazel",
	Args:           []string{"build", "//..."},
	CommandBuilder: &cmdexec.ShellCommandBuilder{},
})
```

### Command Policies and Output Limits

Control which commands are allowed and enforce output size limits:

```go
result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command:          "echo",
	Args:             []string{"hello"},
	CommandValidator: cmdexec.AllowCommands("echo", "ls", "git"),
	MaxStdoutBytes:   1024 * 1024, // 1MB limit
	MaxStderrBytes:   512 * 1024,  // 512KB limit
})
if result != nil && result.StdoutTruncated {
	fmt.Println("stdout was truncated")
}
```

### Streaming Output

Stream stdout/stderr in real-time with `StdoutWriter`/`StderrWriter`:

```go
var buf bytes.Buffer
result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command:      "long-running-command",
	StdoutWriter: &buf, // Stream stdout here
	StderrWriter: os.Stderr, // Stream stderr to terminal
})
// Output is also captured in result.Output and result.Stderr
```

### Concurrent Execution

Run multiple commands in parallel with a configurable concurrency limit:

```go
ce := cmdexec.NewConcurrentExecutor(cmdexec.NewBasicExecutor())
ce.SetMaxConcurrency(4)

configs := []cmdexec.ToolConfig{
	{Command: "echo", Args: []string{"one"}},
	{Command: "echo", Args: []string{"two"}},
	{Command: "echo", Args: []string{"three"}},
}

results, err := ce.ExecuteAll(ctx, configs)
for _, r := range results {
	fmt.Printf("[%d] %s\n", r.Index, r.Result.Output)
}
```

### Signal Handling

`WithSignalHandling` wraps `BasicExecutor` to handle OS signals (SIGINT, SIGTERM, SIGHUP) and cancel running processes gracefully:

```go
executor := cmdexec.NewWithSignalHandling()
ctx, err := executor.Start()
if err != nil {
	log.Fatal(err)
}
defer executor.Stop()

result, err := executor.Execute(ctx, cmdexec.ToolConfig{
	Command: "long-running-process",
})
```

### Helper Functions

Convenience functions that mirror the `os/exec` API:

| Function                    | Description                                        |
| --------------------------- | -------------------------------------------------- |
| `Output`                    | Run a command and return stdout                    |
| `Run`                       | Run a command and return an error on non-zero exit |
| `CombinedOutput`            | Run a command and return combined stdout+stderr    |
| `OutputWithWorkDir`         | Like `Output` with a working directory             |
| `RunWithWorkDir`            | Like `Run` with a working directory                |
| `CombinedOutputWithWorkDir` | Like `CombinedOutput` with a working directory     |
| `OutputWithStdin`           | Like `Output` with stdin input                     |
| `CombinedOutputWithStdin`   | Like `CombinedOutput` with stdin input             |

### Testing with MockExecutor

`MockExecutor` implements the `Executor` interface for tests. It supports expectations with matchers, call history recording, and a fluent builder API.

```go
mock := cmdexec.NewMockExecutor()

// Set up expectations
mock.ExpectCommand("git").
	WillSucceed("abc123\n", 0).
	Once().
	Build()

mock.ExpectCommandWithArgs("git", "status").
	WillSucceed("On branch main\n", 0).
	Build()

// Use in code under test
result, err := mock.Execute(ctx, cmdexec.ToolConfig{Command: "git"})

// Verify expectations were met
if err := mock.AssertExpectationsMet(); err != nil {
	t.Fatal(err)
}
```

### Error Types

| Type                      | Description                                  |
| ------------------------- | -------------------------------------------- |
| `ValidationError`         | Invalid `ToolConfig` fields                  |
| `TimeoutError`            | Command exceeded its timeout                 |
| `ExecutableNotFoundError` | Command not found in PATH                    |
| `RetryExhaustedError`     | All retry attempts failed (wraps last error) |
| `ExitError`               | Non-zero exit code from helper functions     |
| `SignalHandlerError`      | Signal handler lifecycle errors              |
| `CommandNotAllowedError`  | Command rejected by CommandValidator         |
| `OutputLimitError`        | Output exceeded configured size limit        |

#### Execute Error Contract

`Execute` returns errors using a specific contract:

- **Transport/system errors** return `(nil, error)` with typed errors (e.g., `TimeoutError`, `CommandNotAllowedError`)
- **Process exits** return `(*ExecutionResult, nil)` with `ExitCode` set in result
- **Retry exhaustion** returns `(nil, *RetryExhaustedError)` with `LastResult` field for diagnostics

```go
result, err := executor.Execute(ctx, config)
if err != nil {
	var retryErr *cmdexec.RetryExhaustedError
	if errors.As(err, &retryErr) && retryErr.LastResult != nil {
		// Access final attempt's structured diagnostics
		fmt.Printf("Exit code: %d, Stderr: %s\n",
			retryErr.LastResult.ExitCode, retryErr.LastResult.Stderr)
	}
}
```

### JSON Serialization

`ExecutionResult` supports custom JSON marshaling with RFC3339Nano timestamps and a computed `duration` field, making it suitable for structured logging and storage.

## Development

```bash
make all          # format, fix, test, vet
make check        # CI-friendly: check-format, lint, test, vet
make test         # run tests
make lint         # run golangci-lint
make coverage     # generate coverage profile
```

## License

See [LICENSE](LICENSE) for details.
