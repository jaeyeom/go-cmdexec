package cmdexec

import (
	"fmt"
	"io"
	"time"
)

// ToolConfig represents the configuration for executing a tool.
type ToolConfig struct {
	// Command is the executable command to run
	Command string

	// Args are the arguments to pass to the command
	Args []string

	// WorkingDir is the directory where the command should be executed
	// If empty, uses the current working directory
	WorkingDir string

	// Timeout is the maximum duration to allow the command to run
	// If zero, no timeout is applied
	Timeout time.Duration

	// MaxRetries is the maximum number of retry attempts for flaky tools
	MaxRetries int

	// RetryDelay is the delay between retry attempts
	RetryDelay time.Duration

	// Env contains additional environment variables for the command
	// These will be added to the current environment
	Env map[string]string

	// Stdin is an optional reader for providing input to the command
	// If nil, the command will have no stdin
	Stdin io.Reader

	// CommandBuilder defines how to build the command for execution.
	// If nil, defaults to DirectCommandBuilder for direct execution.
	// Use ShellCommandBuilder for tools that need shell execution (e.g., Bazel, Gradle).
	CommandBuilder CommandBuilder

	// StdoutWriter is an optional writer for streaming stdout during execution.
	// When set, process stdout is tee'd to both this writer and the internal
	// buffer (ExecutionResult.Output is still populated).
	// The caller is responsible for thread-safety of the provided writer.
	StdoutWriter io.Writer

	// StderrWriter is an optional writer for streaming stderr during execution.
	// When set, process stderr is tee'd to both this writer and the internal
	// buffer (ExecutionResult.Stderr is still populated).
	// The caller is responsible for thread-safety of the provided writer.
	StderrWriter io.Writer

	// CommandValidator is an optional function that validates whether the
	// command is allowed to execute. It receives the command name and args.
	// Return a non-nil error to block execution. If nil, all commands are allowed.
	CommandValidator func(command string, args []string) error

	// MaxStdoutBytes limits the maximum number of bytes captured from stdout.
	// When exceeded, output is truncated and ExecutionResult.StdoutTruncated
	// is set to true. Zero means no limit.
	MaxStdoutBytes int64

	// MaxStderrBytes limits the maximum number of bytes captured from stderr.
	// When exceeded, output is truncated and ExecutionResult.StderrTruncated
	// is set to true. Zero means no limit.
	MaxStderrBytes int64
}

// Validate ensures the ToolConfig has valid data.
func (tc *ToolConfig) Validate() error {
	if tc.Command == "" {
		return &ValidationError{Field: "Command", Message: "command cannot be empty"}
	}

	if tc.MaxRetries < 0 {
		return &ValidationError{Field: "MaxRetries", Message: "maxRetries cannot be negative"}
	}

	if tc.RetryDelay < 0 {
		return &ValidationError{Field: "RetryDelay", Message: "retryDelay cannot be negative"}
	}

	if tc.Timeout < 0 {
		return &ValidationError{Field: "Timeout", Message: "timeout cannot be negative"}
	}

	if tc.MaxStdoutBytes < 0 {
		return &ValidationError{Field: "MaxStdoutBytes", Message: "maxStdoutBytes cannot be negative"}
	}

	if tc.MaxStderrBytes < 0 {
		return &ValidationError{Field: "MaxStderrBytes", Message: "maxStderrBytes cannot be negative"}
	}

	if tc.CommandValidator != nil {
		if err := tc.CommandValidator(tc.Command, tc.Args); err != nil {
			return &CommandNotAllowedError{
				Command: tc.Command,
				Reason:  err.Error(),
			}
		}
	}

	return nil
}

// Error types for different failure scenarios

// ValidationError represents a validation failure in tool configuration.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return "validation error in field '" + e.Field + "': " + e.Message
}

// TimeoutError represents a timeout during command execution.
type TimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return "command '" + e.Command + "' timed out after " + e.Timeout.String()
}

// ExecutableNotFoundError represents a missing executable.
type ExecutableNotFoundError struct {
	Command string
}

func (e *ExecutableNotFoundError) Error() string {
	return "executable not found: " + e.Command
}

// CommandNotAllowedError is returned when a command fails the CommandValidator check.
type CommandNotAllowedError struct {
	Command string
	Reason  string
}

func (e *CommandNotAllowedError) Error() string {
	return fmt.Sprintf("command %q not allowed: %s", e.Command, e.Reason)
}

// OutputLimitError is returned when output exceeds configured MaxStdoutBytes
// or MaxStderrBytes limits. Note: this error is not returned by Execute itself
// (which signals truncation via ExecutionResult fields), but is provided for
// callers who need a typed error for limit violations in custom pipelines.
type OutputLimitError struct {
	Stream string // "stdout" or "stderr"
	Limit  int64
}

func (e *OutputLimitError) Error() string {
	return fmt.Sprintf("%s output exceeded limit of %d bytes", e.Stream, e.Limit)
}

// RetryExhaustedError represents failure after all retry attempts.
type RetryExhaustedError struct {
	Command   string
	Attempts  int
	LastError error
}

func (e *RetryExhaustedError) Error() string {
	return fmt.Sprintf("retry exhausted for command %q after %d attempts. Last error: %v",
		e.Command, e.Attempts, e.LastError)
}

// Unwrap returns the last error for error chain compatibility.
func (e *RetryExhaustedError) Unwrap() error {
	return e.LastError
}

// AllowCommands returns a CommandValidator that only allows the specified
// command names. Any command not in the list will be rejected.
func AllowCommands(allowed ...string) func(string, []string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, cmd := range allowed {
		set[cmd] = struct{}{}
	}
	return func(command string, _ []string) error {
		if _, ok := set[command]; !ok {
			return fmt.Errorf("not in allowlist")
		}
		return nil
	}
}
