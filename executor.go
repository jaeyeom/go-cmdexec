package cmdexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BasicExecutor handles the execution of external tools and commands.
type BasicExecutor struct{}

// NewBasicExecutor creates a new BasicExecutor instance.
func NewBasicExecutor() *BasicExecutor {
	return &BasicExecutor{}
}

// Execute runs a tool with the given configuration and returns the result.
//
// Error contract:
//   - Transport/system errors (timeout, executable not found, context
//     cancellation, I/O failures) return (nil, error) with typed errors.
//   - Process exit outcomes return (*ExecutionResult, nil). The caller
//     inspects ExitCode to determine success or failure.
//
// Typed errors returned:
//   - *ValidationError: invalid ToolConfig fields.
//   - *TimeoutError: command exceeded configured Timeout.
//   - *ExecutableNotFoundError: command not found in PATH.
//   - *RetryExhaustedError: all retry attempts failed (wraps last error).
//   - context.Canceled / context.DeadlineExceeded: context was cancelled.
func (e *BasicExecutor) Execute(ctx context.Context, cfg ToolConfig) (*ExecutionResult, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Fast path: no retries configured
	if cfg.MaxRetries == 0 {
		return e.executeOnce(ctx, cfg)
	}

	// Retry loop
	maxAttempts := 1 + cfg.MaxRetries
	var lastResult *ExecutionResult
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := e.executeOnce(ctx, cfg)

		// Success case
		if err == nil && result.ExitCode == 0 {
			return result, nil
		}

		// Non-retryable error: executable not found
		if _, isNotFound := err.(*ExecutableNotFoundError); isNotFound {
			return nil, err
		}

		// Abort retries on context cancellation/timeout
		if ctx.Err() != nil {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("context done: %w", ctx.Err())
		}

		// Store last attempt for final error reporting
		lastResult = result
		lastErr = err

		// If not the last attempt, sleep with context awareness
		if attempt < maxAttempts {
			if cfg.RetryDelay > 0 {
				select {
				case <-time.After(cfg.RetryDelay):
					// Continue to next attempt
				case <-ctx.Done():
					// Context cancelled during retry delay
					return nil, fmt.Errorf("context done during retry delay: %w", ctx.Err())
				}
			}
		}
	}

	// All attempts exhausted, construct final error
	if lastErr != nil {
		return nil, &RetryExhaustedError{
			Command:   buildCommandString(cfg.Command, cfg.Args),
			Attempts:  maxAttempts,
			LastError: lastErr,
		}
	}

	// Last attempt returned non-zero exit code without error
	finalErr := fmt.Errorf("command exited with code %d", lastResult.ExitCode)
	if lastResult.Error != "" {
		finalErr = fmt.Errorf("%s", lastResult.Error)
	}
	return nil, &RetryExhaustedError{
		Command:   buildCommandString(cfg.Command, cfg.Args),
		Attempts:  maxAttempts,
		LastError: finalErr,
	}
}

// executeOnce performs a single execution attempt.
func (e *BasicExecutor) executeOnce(ctx context.Context, cfg ToolConfig) (*ExecutionResult, error) {
	execCtx, cancel := e.createExecutionContext(ctx, cfg.Timeout)
	if cancel != nil {
		defer cancel()
	}

	cmd := e.createCommand(execCtx, cfg)
	e.setupCommand(cmd, cfg)

	slog.Debug("Executing command",
		"command", cfg.Command,
		"args", cfg.Args,
		"working_dir", cfg.WorkingDir)

	cr := e.executeCommand(cmd, cfg)

	if timedOut := e.handleTimeout(execCtx, cr.err, cfg); timedOut {
		return nil, &TimeoutError{
			Command: buildCommandString(cfg.Command, cfg.Args),
			Timeout: cfg.Timeout,
		}
	}

	exitCode, err := e.processExecutionError(cr.err, cfg.Command)
	if err != nil {
		return nil, err
	}

	return e.buildExecutionResult(cfg, cr, exitCode), nil
}

func (e *BasicExecutor) createExecutionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, nil
}

func (e *BasicExecutor) createCommand(ctx context.Context, cfg ToolConfig) *exec.Cmd {
	// Use the configured CommandBuilder, defaulting to DirectCommandBuilder
	builder := cfg.CommandBuilder
	if builder == nil {
		builder = &DirectCommandBuilder{}
	}
	return builder.Build(ctx, cfg.Command, cfg.Args)
}

func (e *BasicExecutor) setupCommand(cmd *exec.Cmd, cfg ToolConfig) {
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}

	if len(cfg.Env) > 0 {
		cmd.Env = os.Environ()
		for key, value := range cfg.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	if cfg.Stdin != nil {
		cmd.Stdin = cfg.Stdin
	}
}

type executeCommandResult struct {
	stdout, stderr           bytes.Buffer
	startTime, endTime       time.Time
	stdoutTrunc, stderrTrunc bool
	err                      error
}

func (e *BasicExecutor) executeCommand(cmd *exec.Cmd, cfg ToolConfig) executeCommandResult {
	var r executeCommandResult
	var stdoutW, stderrW io.Writer = &r.stdout, &r.stderr

	// Apply output size limits
	var stdoutLW, stderrLW *limitedWriter
	if cfg.MaxStdoutBytes > 0 {
		stdoutLW = &limitedWriter{w: &r.stdout, n: cfg.MaxStdoutBytes}
		stdoutW = stdoutLW
	}
	if cfg.MaxStderrBytes > 0 {
		stderrLW = &limitedWriter{w: &r.stderr, n: cfg.MaxStderrBytes}
		stderrW = stderrLW
	}

	// Apply streaming writers via tee
	if cfg.StdoutWriter != nil {
		stdoutW = io.MultiWriter(stdoutW, cfg.StdoutWriter)
	}
	if cfg.StderrWriter != nil {
		stderrW = io.MultiWriter(stderrW, cfg.StderrWriter)
	}

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	r.startTime = time.Now()
	r.err = cmd.Run()
	r.endTime = time.Now()

	if stdoutLW != nil {
		r.stdoutTrunc = stdoutLW.truncated
	}
	if stderrLW != nil {
		r.stderrTrunc = stderrLW.truncated
	}

	return r
}

// limitedWriter wraps a writer and stops writing after n bytes,
// silently discarding excess data while tracking truncation.
type limitedWriter struct {
	w         io.Writer
	n         int64 // bytes remaining
	truncated bool
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.n <= 0 {
		lw.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > lw.n {
		lw.truncated = true
		n, err := lw.w.Write(p[:lw.n])
		lw.n = 0
		if err != nil {
			return n, err //nolint:wrapcheck
		}
		return len(p), nil
	}
	n, err := lw.w.Write(p)
	lw.n -= int64(n)
	return n, err //nolint:wrapcheck
}

func (e *BasicExecutor) handleTimeout(ctx context.Context, err error, cfg ToolConfig) bool {
	return err != nil && ctx.Err() == context.DeadlineExceeded && cfg.Timeout > 0
}

func (e *BasicExecutor) processExecutionError(err error, command string) (int, error) {
	if err == nil {
		return 0, nil
	}

	if errors.Is(err, exec.ErrNotFound) {
		return 0, &ExecutableNotFoundError{Command: command}
	}

	// Context cancellation is a system-level error, not a process exit.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, err
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}

	// Unknown execution errors (I/O failures, permission errors, etc.)
	// are returned rather than silently converted to exit code -1.
	return 0, fmt.Errorf("command %q: %w", command, err)
}

func (e *BasicExecutor) buildExecutionResult(cfg ToolConfig, cr executeCommandResult, exitCode int) *ExecutionResult {
	return &ExecutionResult{
		Command:         cfg.Command,
		Args:            cfg.Args,
		WorkingDir:      cfg.WorkingDir,
		Output:          cr.stdout.String(),
		Stderr:          cr.stderr.String(),
		ExitCode:        exitCode,
		StartTime:       cr.startTime,
		EndTime:         cr.endTime,
		TimedOut:        false,
		StdoutTruncated: cr.stdoutTrunc,
		StderrTruncated: cr.stderrTrunc,
	}
}

// IsAvailable checks if a command is available in the system PATH.
func (e *BasicExecutor) IsAvailable(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

// buildCommandString constructs a shell-like command string for display purposes.
func buildCommandString(command string, args []string) string {
	parts := []string{command}
	for _, arg := range args {
		// Simple quoting for args with spaces
		if strings.Contains(arg, " ") {
			parts = append(parts, fmt.Sprintf("%q", arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}
