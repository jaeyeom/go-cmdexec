package cmdexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewBasicExecutor(t *testing.T) {
	executor := NewBasicExecutor()
	if executor == nil {
		t.Fatal("NewBasicExecutor() returned nil")
	}
}

func TestBasicExecutor_Execute(t *testing.T) {
	executor := NewBasicExecutor()
	ctx := context.Background()

	tests := []struct {
		name        string
		config      ToolConfig
		wantErr     bool
		checkOutput func(t *testing.T, output, stderr string, exitCode int)
	}{
		{
			name: "simple echo command",
			config: ToolConfig{
				Command: "echo",
				Args:    []string{"hello", "world"},
			},
			wantErr: false,
			checkOutput: func(t *testing.T, output, stderr string, exitCode int) {
				expectedOutput := "hello world\n"
				if output != expectedOutput {
					t.Errorf("output = %q, want %q", output, expectedOutput)
				}
				if stderr != "" {
					t.Errorf("stderr = %q, want empty", stderr)
				}
				if exitCode != 0 {
					t.Errorf("exitCode = %d, want 0", exitCode)
				}
			},
		},
		{
			name: "command with working directory",
			config: ToolConfig{
				Command:    "pwd",
				WorkingDir: "/tmp",
			},
			wantErr: false,
			checkOutput: func(t *testing.T, output, _ string, exitCode int) {
				if runtime.GOOS != "windows" {
					expectedOutput := "/tmp\n"
					if output != expectedOutput {
						t.Errorf("output = %q, want %q", output, expectedOutput)
					}
				}
				if exitCode != 0 {
					t.Errorf("exitCode = %d, want 0", exitCode)
				}
			},
		},
		{
			name: "command with environment variables",
			config: ToolConfig{
				Command: "sh",
				Args:    []string{"-c", "echo $TEST_VAR"},
				Env:     map[string]string{"TEST_VAR": "test_value"},
			},
			wantErr: false,
			checkOutput: func(t *testing.T, output, _ string, exitCode int) {
				expectedOutput := "test_value\n"
				if output != expectedOutput {
					t.Errorf("output = %q, want %q", output, expectedOutput)
				}
				if exitCode != 0 {
					t.Errorf("exitCode = %d, want 0", exitCode)
				}
			},
		},
		{
			name: "command with non-zero exit code",
			config: ToolConfig{
				Command: "sh",
				Args:    []string{"-c", "exit 42"},
			},
			wantErr: false,
			checkOutput: func(t *testing.T, _, _ string, exitCode int) {
				if exitCode != 42 {
					t.Errorf("exitCode = %d, want 42", exitCode)
				}
			},
		},
		{
			name: "command that writes to stderr",
			config: ToolConfig{
				Command: "sh",
				Args:    []string{"-c", "echo error >&2"},
			},
			wantErr: false,
			checkOutput: func(t *testing.T, _, stderr string, exitCode int) {
				expectedStderr := "error\n"
				if stderr != expectedStderr {
					t.Errorf("stderr = %q, want %q", stderr, expectedStderr)
				}
				if exitCode != 0 {
					t.Errorf("exitCode = %d, want 0", exitCode)
				}
			},
		},
		{
			name: "nonexistent command",
			config: ToolConfig{
				Command: "nonexistent-command-that-should-not-exist",
			},
			wantErr: true,
		},
		{
			name: "invalid config - empty command",
			config: ToolConfig{
				Command: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executor.Execute(ctx, tt.config)

			if tt.wantErr {
				if err == nil && result != nil && result.ExitCode == 0 {
					t.Errorf("Execute() error = nil, wantErr = true")
				}
				return
			}

			if err != nil {
				t.Errorf("Execute() unexpected error = %v", err)
				return
			}

			if result == nil {
				t.Fatal("Execute() returned nil result")
				return
			}

			// Verify basic fields
			if result.Command != tt.config.Command {
				t.Errorf("Command = %q, want %q", result.Command, tt.config.Command)
			}

			if len(result.Args) != len(tt.config.Args) {
				t.Errorf("Args length = %d, want %d", len(result.Args), len(tt.config.Args))
			} else {
				for i, arg := range result.Args {
					if arg != tt.config.Args[i] {
						t.Errorf("Args[%d] = %q, want %q", i, arg, tt.config.Args[i])
					}
				}
			}

			if result.WorkingDir != tt.config.WorkingDir {
				t.Errorf("WorkingDir = %q, want %q", result.WorkingDir, tt.config.WorkingDir)
			}

			// Verify timing
			if result.StartTime.IsZero() {
				t.Error("StartTime is zero")
			}
			if result.EndTime.IsZero() {
				t.Error("EndTime is zero")
			}
			if result.EndTime.Before(result.StartTime) {
				t.Error("EndTime is before StartTime")
			}
			if result.Duration() < 0 {
				t.Errorf("Duration() = %v, want positive", result.Duration())
			}

			// Check custom output validation
			if tt.checkOutput != nil {
				tt.checkOutput(t, result.Output, result.Stderr, result.ExitCode)
			}
		})
	}
}

func TestBasicExecutor_Execute_Context(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping context cancellation test on Windows")
	}

	executor := NewBasicExecutor()

	// Test context cancellation
	ctx, cancel := context.WithCancel(context.Background())

	toolConfig := ToolConfig{
		Command: "sleep",
		Args:    []string{"10"},
	}

	// Start execution in a goroutine
	done := make(chan struct{})
	var result *ExecutionResult
	var err error

	go func() {
		result, err = executor.Execute(ctx, toolConfig)
		close(done)
	}()

	// Give the command time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for execution to complete
	select {
	case <-done:
		// Command completed (was cancelled)
	case <-time.After(2 * time.Second):
		t.Fatal("Execute() did not respond to context cancellation")
	}

	// The command should have been interrupted
	if err == nil && result != nil && result.ExitCode == 0 {
		t.Error("Expected non-zero exit code for cancelled command")
	}
}

func TestBasicExecutor_Execute_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping timeout test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	tests := []struct {
		name           string
		config         ToolConfig
		wantErr        bool
		wantTimeoutErr bool
		checkResult    func(t *testing.T, result *ExecutionResult, err error)
	}{
		{
			name: "command with timeout that completes in time",
			config: ToolConfig{
				Command: "sleep",
				Args:    []string{"0.1"},
				Timeout: 1 * time.Second,
			},
			wantErr:        false,
			wantTimeoutErr: false,
			checkResult: func(t *testing.T, result *ExecutionResult, _ error) {
				if result == nil {
					t.Fatal("Expected result, got nil")
					return
				}
				if result.TimedOut {
					t.Error("Expected TimedOut to be false")
				}
				if result.ExitCode != 0 {
					t.Errorf("Expected exit code 0, got %d", result.ExitCode)
				}
			},
		},
		{
			name: "command with timeout that times out",
			config: ToolConfig{
				Command: "sleep",
				Args:    []string{"2"},
				Timeout: 200 * time.Millisecond,
			},
			wantErr:        true,
			wantTimeoutErr: true,
			checkResult: func(t *testing.T, result *ExecutionResult, err error) {
				if result != nil {
					t.Error("Expected nil result for timeout error")
				}

				// Check that we got a TimeoutError
				var timeoutErr *TimeoutError
				if !errors.As(err, &timeoutErr) {
					t.Errorf("Expected TimeoutError, got %T: %v", err, err)
				} else {
					if timeoutErr.Timeout != 200*time.Millisecond {
						t.Errorf("Expected timeout 200ms, got %v", timeoutErr.Timeout)
					}
					if !strings.Contains(timeoutErr.Command, "sleep") {
						t.Errorf("Expected command to contain 'sleep', got %q", timeoutErr.Command)
					}
				}
			},
		},
		{
			name: "command without timeout runs normally",
			config: ToolConfig{
				Command: "echo",
				Args:    []string{"test"},
				Timeout: 0, // No timeout
			},
			wantErr:        false,
			wantTimeoutErr: false,
			checkResult: func(t *testing.T, result *ExecutionResult, _ error) {
				if result == nil {
					t.Fatal("Expected result, got nil")
					return
				}
				if result.TimedOut {
					t.Error("Expected TimedOut to be false")
				}
				if result.ExitCode != 0 {
					t.Errorf("Expected exit code 0, got %d", result.ExitCode)
				}
				if !strings.Contains(result.Output, "test") {
					t.Errorf("Expected output to contain 'test', got %q", result.Output)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			result, err := executor.Execute(ctx, tt.config)
			duration := time.Since(start)

			if tt.wantErr != (err != nil) {
				t.Errorf("Execute() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// For timeout tests, verify the timing
			if tt.wantTimeoutErr && tt.config.Timeout > 0 {
				// Should complete close to the timeout duration
				expectedMax := tt.config.Timeout + 500*time.Millisecond // Allow some overhead
				if duration > expectedMax {
					t.Errorf("Timeout took too long: %v, expected max: %v", duration, expectedMax)
				}
			}

			if tt.checkResult != nil {
				tt.checkResult(t, result, err)
			}
		})
	}
}

func TestBasicExecutor_IsAvailable(t *testing.T) {
	executor := NewBasicExecutor()

	tests := []struct {
		name          string
		command       string
		wantAvailable bool
	}{
		{
			name:          "common command - echo",
			command:       "echo",
			wantAvailable: true,
		},
		{
			name:          "common command - sh",
			command:       "sh",
			wantAvailable: runtime.GOOS != "windows",
		},
		{
			name:          "nonexistent command",
			command:       "nonexistent-command-that-should-not-exist",
			wantAvailable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := executor.IsAvailable(tt.command)
			if got != tt.wantAvailable {
				t.Errorf("IsAvailable(%q) = %v, want %v", tt.command, got, tt.wantAvailable)
			}
		})
	}
}

func TestBuildCommandString(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    string
	}{
		{
			name:    "simple command",
			command: "echo",
			args:    []string{"hello"},
			want:    "echo hello",
		},
		{
			name:    "command with multiple args",
			command: "go",
			args:    []string{"test", "./..."},
			want:    "go test ./...",
		},
		{
			name:    "args with spaces",
			command: "echo",
			args:    []string{"hello world", "foo"},
			want:    `echo "hello world" foo`,
		},
		{
			name:    "no args",
			command: "ls",
			args:    []string{},
			want:    "ls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCommandString(tt.command, tt.args)
			if got != tt.want {
				t.Errorf("buildCommandString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBasicExecutor_Execute_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping permission test on Windows")
	}

	// Create a test file without execute permissions
	tmpFile, err := os.CreateTemp("", "test-no-exec-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Write a simple script
	if _, err := tmpFile.WriteString("#!/bin/sh\necho test\n"); err != nil {
		t.Fatal(err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	// Make sure it's not executable
	if err := os.Chmod(tmpFile.Name(), 0o644); err != nil {
		t.Fatal(err)
	}

	executor := NewBasicExecutor()
	toolConfig := ToolConfig{
		Command: tmpFile.Name(),
	}

	result, err := executor.Execute(context.Background(), toolConfig)
	if err == nil && result != nil && result.ExitCode == 0 {
		t.Error("Expected error for non-executable file")
	}
}

func TestBasicExecutor_Execute_TimeoutTiming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping timeout timing test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	// Test that timeout is enforced accurately
	config := ToolConfig{
		Command: "sleep",
		Args:    []string{"5"},          // Sleep for 5 seconds
		Timeout: 500 * time.Millisecond, // But timeout after 500ms
	}

	start := time.Now()
	result, err := executor.Execute(ctx, config)
	duration := time.Since(start)

	// Should have timed out
	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}

	// Should return TimeoutError
	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Expected TimeoutError, got %T: %v", err, err)
	}

	// Should not return a result
	if result != nil {
		t.Error("Expected nil result for timeout")
	}

	// Should complete within reasonable time of the timeout
	expectedMin := 500 * time.Millisecond
	expectedMax := 1000 * time.Millisecond // Allow some overhead

	if duration < expectedMin {
		t.Errorf("Command completed too quickly: %v, expected at least: %v", duration, expectedMin)
	}
	if duration > expectedMax {
		t.Errorf("Command took too long: %v, expected at most: %v", duration, expectedMax)
	}
}

func TestBasicExecutor_Execute_RetrySuccessAfterFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping retry test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	// Create a temp file as a counter. The shell script increments the
	// count and fails until the count reaches 3 (the 3rd attempt).
	counterFile, err := os.CreateTemp("", "retry-counter-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(counterFile.Name()) }()
	if _, err := counterFile.WriteString("0"); err != nil {
		t.Fatal(err)
	}
	if err := counterFile.Close(); err != nil {
		t.Fatal(err)
	}

	// Script: read counter, increment, write back, exit 1 if < 3, else exit 0
	script := `count=$(cat ` + counterFile.Name() + `); count=$((count+1)); echo $count > ` + counterFile.Name() + `; if [ $count -lt 3 ]; then exit 1; fi; echo success`

	cfg := ToolConfig{
		Command:    "sh",
		Args:       []string{"-c", script},
		MaxRetries: 4, // Up to 5 attempts total, should succeed on 3rd
		RetryDelay: 10 * time.Millisecond,
	}

	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() unexpected error = %v", err)
	}
	if result == nil {
		t.Fatal("Execute() returned nil result")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Output, "success") {
		t.Errorf("Output = %q, want to contain 'success'", result.Output)
	}
}

func TestBasicExecutor_Execute_RetryExhausted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping retry test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	cfg := ToolConfig{
		Command:    "sh",
		Args:       []string{"-c", "exit 1"},
		MaxRetries: 2, // 3 total attempts, all fail
		RetryDelay: 10 * time.Millisecond,
	}

	result, err := executor.Execute(ctx, cfg)
	if err == nil {
		t.Fatal("Execute() expected error, got nil")
	}
	if result != nil {
		t.Errorf("Execute() expected nil result, got %+v", result)
	}

	var retryErr *RetryExhaustedError
	if !errors.As(err, &retryErr) {
		t.Fatalf("Expected RetryExhaustedError, got %T: %v", err, err)
	}
	if retryErr.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", retryErr.Attempts)
	}
	if !strings.Contains(retryErr.Command, "sh") {
		t.Errorf("Command = %q, want to contain 'sh'", retryErr.Command)
	}
	if retryErr.LastError == nil {
		t.Error("LastError is nil, want non-nil")
	}
}

func TestBasicExecutor_Execute_RetryContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping retry test on Windows")
	}

	executor := NewBasicExecutor()

	// Use a long retry delay so the context cancel fires during sleep
	cfg := ToolConfig{
		Command:    "sh",
		Args:       []string{"-c", "exit 1"},
		MaxRetries: 100,
		RetryDelay: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var result *ExecutionResult
	var err error

	go func() {
		result, err = executor.Execute(ctx, cfg)
		close(done)
	}()

	// Give time for first attempt to fail and enter retry delay
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good, returned promptly
	case <-time.After(2 * time.Second):
		t.Fatal("Execute() did not respond to context cancellation")
	}

	if err == nil {
		t.Fatal("Expected error after context cancellation")
	}
	if result != nil {
		t.Error("Expected nil result after context cancellation")
	}
}

func TestBasicExecutor_Execute_RetryDelayTiming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping retry test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	retryDelay := 100 * time.Millisecond
	cfg := ToolConfig{
		Command:    "sh",
		Args:       []string{"-c", "exit 1"},
		MaxRetries: 2, // 3 attempts = 2 delays
		RetryDelay: retryDelay,
	}

	start := time.Now()
	_, _ = executor.Execute(ctx, cfg)
	duration := time.Since(start)

	// 2 delays of 100ms each = at least 200ms
	expectedMin := 2 * retryDelay
	if duration < expectedMin {
		t.Errorf("Duration = %v, want at least %v (2 retry delays)", duration, expectedMin)
	}
}

func TestBasicExecutor_Execute_NoRetryOnZeroMaxRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping retry test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	// With MaxRetries=0, a failing command should return result, nil (not RetryExhaustedError)
	cfg := ToolConfig{
		Command:    "sh",
		Args:       []string{"-c", "exit 1"},
		MaxRetries: 0,
	}

	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() with MaxRetries=0 unexpected error = %v", err)
	}
	if result == nil {
		t.Fatal("Execute() returned nil result")
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
}

func TestBasicExecutor_Execute_RetryNotFoundNotRetried(t *testing.T) {
	executor := NewBasicExecutor()
	ctx := context.Background()

	cfg := ToolConfig{
		Command:    "nonexistent-command-that-should-not-exist",
		MaxRetries: 3,
		RetryDelay: 10 * time.Millisecond,
	}

	start := time.Now()
	_, err := executor.Execute(ctx, cfg)
	duration := time.Since(start)

	if err == nil {
		t.Fatal("Expected error for nonexistent command")
	}

	var notFoundErr *ExecutableNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("Expected ExecutableNotFoundError, got %T: %v", err, err)
	}

	// Should return immediately without any retry delays
	if duration > 500*time.Millisecond {
		t.Errorf("Duration = %v, expected immediate return (no retries)", duration)
	}
}

// TestBasicExecutor_Execute_ErrorContract verifies the Execute error contract:
// - System errors return (nil, error) with typed errors.
// - Process exit outcomes return (*ExecutionResult, nil) with ExitCode.
func TestBasicExecutor_Execute_ErrorContract(t *testing.T) {
	executor := NewBasicExecutor()
	ctx := context.Background()

	t.Run("non-zero exit returns result not error", func(t *testing.T) {
		result, err := executor.Execute(ctx, ToolConfig{
			Command: "sh",
			Args:    []string{"-c", "exit 42"},
		})
		if err != nil {
			t.Fatalf("Execute() returned error %v, want nil error for non-zero exit", err)
		}
		if result == nil {
			t.Fatal("Execute() returned nil result")
		}
		if result.ExitCode != 42 {
			t.Errorf("ExitCode = %d, want 42", result.ExitCode)
		}
	})

	t.Run("missing executable returns typed error", func(t *testing.T) {
		result, err := executor.Execute(ctx, ToolConfig{
			Command: "nonexistent-command-that-should-not-exist",
		})
		if err == nil {
			t.Fatal("Execute() returned nil error for missing executable")
		}
		if result != nil {
			t.Error("Execute() returned non-nil result for missing executable")
		}
		var notFoundErr *ExecutableNotFoundError
		if !errors.As(err, &notFoundErr) {
			t.Errorf("Expected ExecutableNotFoundError, got %T: %v", err, err)
		}
	})

	t.Run("timeout returns typed error", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping timeout test on Windows")
		}
		result, err := executor.Execute(ctx, ToolConfig{
			Command: "sleep",
			Args:    []string{"5"},
			Timeout: 100 * time.Millisecond,
		})
		if err == nil {
			t.Fatal("Execute() returned nil error for timeout")
		}
		if result != nil {
			t.Error("Execute() returned non-nil result for timeout")
		}
		var timeoutErr *TimeoutError
		if !errors.As(err, &timeoutErr) {
			t.Errorf("Expected TimeoutError, got %T: %v", err, err)
		}
	})

	t.Run("context cancellation returns error", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping context test on Windows")
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var result *ExecutionResult
		var err error

		go func() {
			result, err = executor.Execute(ctx, ToolConfig{
				Command: "sleep",
				Args:    []string{"10"},
			})
			close(done)
		}()

		time.Sleep(100 * time.Millisecond)
		cancel()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Execute() did not respond to context cancellation")
		}

		// Context cancellation is a system error → (nil, error)
		if err == nil {
			// On some systems, the process might exit with a signal
			// before the context error propagates, returning a result
			// with non-zero exit code. Both behaviors are acceptable.
			if result == nil || result.ExitCode == 0 {
				t.Error("Expected either error or non-zero exit code for cancelled command")
			}
		}
	})

	t.Run("validation error returns typed error", func(t *testing.T) {
		result, err := executor.Execute(ctx, ToolConfig{
			Command: "",
		})
		if err == nil {
			t.Fatal("Execute() returned nil error for empty command")
		}
		if result != nil {
			t.Error("Execute() returned non-nil result for validation error")
		}
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("Expected ValidationError, got %T: %v", err, err)
		}
	})
}

func TestBasicExecutor_Execute_RetryWithTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping retry test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	// Create a counter file to track attempts
	counterFile, err := os.CreateTemp("", "retry-timeout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(counterFile.Name()) }()
	if _, err := counterFile.WriteString("0"); err != nil {
		t.Fatal(err)
	}
	if err := counterFile.Close(); err != nil {
		t.Fatal(err)
	}

	// Script that times out on first attempt, succeeds on second
	script := `count=$(cat ` + counterFile.Name() + `); count=$((count+1)); echo $count > ` + counterFile.Name() + `; if [ $count -lt 2 ]; then sleep 5; fi; echo done`

	cfg := ToolConfig{
		Command:    "sh",
		Args:       []string{"-c", script},
		Timeout:    200 * time.Millisecond,
		MaxRetries: 2,
		RetryDelay: 10 * time.Millisecond,
	}

	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() unexpected error = %v", err)
	}
	if result == nil {
		t.Fatal("Execute() returned nil result")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestBasicExecutor_Execute_StdoutWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping streaming test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	var streamed bytes.Buffer
	cfg := ToolConfig{
		Command:      "echo",
		Args:         []string{"hello streaming"},
		StdoutWriter: &streamed,
	}

	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Both the result buffer and the streaming writer should have the output
	if result.Output != "hello streaming\n" {
		t.Errorf("result.Output = %q, want %q", result.Output, "hello streaming\n")
	}
	if streamed.String() != "hello streaming\n" {
		t.Errorf("streamed output = %q, want %q", streamed.String(), "hello streaming\n")
	}
}

func TestBasicExecutor_Execute_StderrWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping streaming test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	var streamed bytes.Buffer
	cfg := ToolConfig{
		Command:      "sh",
		Args:         []string{"-c", "echo error-output >&2"},
		StderrWriter: &streamed,
	}

	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Stderr != "error-output\n" {
		t.Errorf("result.Stderr = %q, want %q", result.Stderr, "error-output\n")
	}
	if streamed.String() != "error-output\n" {
		t.Errorf("streamed stderr = %q, want %q", streamed.String(), "error-output\n")
	}
}

func TestBasicExecutor_Execute_BothWriters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping streaming test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	var streamedOut, streamedErr bytes.Buffer
	cfg := ToolConfig{
		Command:      "sh",
		Args:         []string{"-c", "echo stdout-data; echo stderr-data >&2"},
		StdoutWriter: &streamedOut,
		StderrWriter: &streamedErr,
	}

	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Internal buffers preserved
	if result.Output != "stdout-data\n" {
		t.Errorf("result.Output = %q, want %q", result.Output, "stdout-data\n")
	}
	if result.Stderr != "stderr-data\n" {
		t.Errorf("result.Stderr = %q, want %q", result.Stderr, "stderr-data\n")
	}

	// Streaming writers received the same data
	if streamedOut.String() != "stdout-data\n" {
		t.Errorf("streamed stdout = %q, want %q", streamedOut.String(), "stdout-data\n")
	}
	if streamedErr.String() != "stderr-data\n" {
		t.Errorf("streamed stderr = %q, want %q", streamedErr.String(), "stderr-data\n")
	}
}

func TestBasicExecutor_Execute_NilWritersPreserveBehavior(t *testing.T) {
	executor := NewBasicExecutor()
	ctx := context.Background()

	// Without writers, behavior should be unchanged
	cfg := ToolConfig{
		Command: "echo",
		Args:    []string{"unchanged"},
	}

	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output != "unchanged\n" {
		t.Errorf("result.Output = %q, want %q", result.Output, "unchanged\n")
	}
}

func TestBasicExecutor_Execute_CommandValidator(t *testing.T) {
	executor := NewBasicExecutor()
	ctx := context.Background()

	t.Run("allowed command executes normally", func(t *testing.T) {
		cfg := ToolConfig{
			Command:          "echo",
			Args:             []string{"hello"},
			CommandValidator: AllowCommands("echo", "cat"),
		}
		result, err := executor.Execute(ctx, cfg)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Output != "hello\n" {
			t.Errorf("Output = %q, want %q", result.Output, "hello\n")
		}
	})

	t.Run("blocked command returns typed error", func(t *testing.T) {
		cfg := ToolConfig{
			Command:          "rm",
			Args:             []string{"-rf", "/"},
			CommandValidator: AllowCommands("echo", "cat"),
		}
		result, err := executor.Execute(ctx, cfg)
		if err == nil {
			t.Fatal("Execute() expected error for blocked command")
		}
		if result != nil {
			t.Error("Execute() expected nil result for blocked command")
		}
		var notAllowed *CommandNotAllowedError
		if !errors.As(err, &notAllowed) {
			t.Fatalf("Expected CommandNotAllowedError, got %T: %v", err, err)
		}
		if notAllowed.Command != "rm" {
			t.Errorf("Command = %q, want %q", notAllowed.Command, "rm")
		}
	})

	t.Run("custom validator", func(t *testing.T) {
		cfg := ToolConfig{
			Command: "dangerous-cmd",
			CommandValidator: func(cmd string, _ []string) error {
				if cmd == "dangerous-cmd" {
					return fmt.Errorf("dangerous commands are forbidden")
				}
				return nil
			},
		}
		_, err := executor.Execute(ctx, cfg)
		if err == nil {
			t.Fatal("Execute() expected error for custom validator rejection")
		}
		var notAllowed *CommandNotAllowedError
		if !errors.As(err, &notAllowed) {
			t.Fatalf("Expected CommandNotAllowedError, got %T: %v", err, err)
		}
		if !strings.Contains(notAllowed.Reason, "dangerous commands are forbidden") {
			t.Errorf("Reason = %q, want to contain 'dangerous commands are forbidden'", notAllowed.Reason)
		}
	})

	t.Run("nil validator allows all commands", func(t *testing.T) {
		cfg := ToolConfig{
			Command:          "echo",
			Args:             []string{"allowed"},
			CommandValidator: nil,
		}
		result, err := executor.Execute(ctx, cfg)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Output != "allowed\n" {
			t.Errorf("Output = %q, want %q", result.Output, "allowed\n")
		}
	})
}

func TestBasicExecutor_Execute_MaxStdoutBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping output limit test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	t.Run("output within limit is not truncated", func(t *testing.T) {
		cfg := ToolConfig{
			Command:        "echo",
			Args:           []string{"short"},
			MaxStdoutBytes: 1000,
		}
		result, err := executor.Execute(ctx, cfg)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Output != "short\n" {
			t.Errorf("Output = %q, want %q", result.Output, "short\n")
		}
		if result.StdoutTruncated {
			t.Error("StdoutTruncated should be false")
		}
	})

	t.Run("output exceeding limit is truncated", func(t *testing.T) {
		cfg := ToolConfig{
			Command:        "sh",
			Args:           []string{"-c", "printf '%0100s'"},
			MaxStdoutBytes: 10,
		}
		result, err := executor.Execute(ctx, cfg)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(result.Output) != 10 {
			t.Errorf("Output length = %d, want 10", len(result.Output))
		}
		if !result.StdoutTruncated {
			t.Error("StdoutTruncated should be true")
		}
	})

	t.Run("zero limit means no limit", func(t *testing.T) {
		cfg := ToolConfig{
			Command:        "sh",
			Args:           []string{"-c", "printf '%0100s'"},
			MaxStdoutBytes: 0,
		}
		result, err := executor.Execute(ctx, cfg)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(result.Output) != 100 {
			t.Errorf("Output length = %d, want 100", len(result.Output))
		}
		if result.StdoutTruncated {
			t.Error("StdoutTruncated should be false")
		}
	})
}

func TestBasicExecutor_Execute_MaxStderrBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping output limit test on Windows")
	}

	executor := NewBasicExecutor()
	ctx := context.Background()

	cfg := ToolConfig{
		Command:        "sh",
		Args:           []string{"-c", "printf '%0100s' >&2"},
		MaxStderrBytes: 10,
	}
	result, err := executor.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Stderr) != 10 {
		t.Errorf("Stderr length = %d, want 10", len(result.Stderr))
	}
	if !result.StderrTruncated {
		t.Error("StderrTruncated should be true")
	}
	// Stdout should be unaffected
	if result.StdoutTruncated {
		t.Error("StdoutTruncated should be false")
	}
}

func TestAllowCommands(t *testing.T) {
	validator := AllowCommands("echo", "cat", "ls")

	if err := validator("echo", nil); err != nil {
		t.Errorf("echo should be allowed: %v", err)
	}
	if err := validator("cat", []string{"file.txt"}); err != nil {
		t.Errorf("cat should be allowed: %v", err)
	}
	if err := validator("rm", nil); err == nil {
		t.Error("rm should not be allowed")
	}
	if err := validator("sh", []string{"-c", "echo"}); err == nil {
		t.Error("sh should not be allowed")
	}
}
