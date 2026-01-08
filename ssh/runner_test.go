package ssh

import (
	"errors"
	"testing"
)

func TestDefaultRunner_IsAlreadyClosedError(t *testing.T) {
	runner := &DefaultRunner{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "use of closed network connection",
			err:      errors.New("use of closed network connection"),
			expected: true,
		},
		{
			name:     "connection already closed",
			err:      errors.New("connection already closed"),
			expected: true,
		},
		{
			name:     "error with closed network in message",
			err:      errors.New("ssh: use of closed network connection"),
			expected: true,
		},
		{
			name:     "error with already closed in message",
			err:      errors.New("ssh: connection already closed"),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("network timeout"),
			expected: false,
		},
		{
			name:     "authentication error",
			err:      errors.New("ssh: unable to authenticate"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runner.IsAlreadyClosedError(tt.err)
			if result != tt.expected {
				t.Errorf("IsAlreadyClosedError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestDefaultRunner_GetClient(t *testing.T) {
	runner := &DefaultRunner{
		client: nil,
	}

	client := runner.GetClient()
	if client != nil {
		t.Errorf("GetClient() = %v, want nil", client)
	}
}

func TestDefaultRunner_Close_NilClient(t *testing.T) {
	runner := &DefaultRunner{
		client: nil,
	}

	err := runner.Close()
	if err != nil {
		t.Errorf("Close() with nil client error = %v, want nil", err)
	}
}

func TestDefaultRunner_Run_NilClient(t *testing.T) {
	runner := &DefaultRunner{
		client: nil,
	}

	_, err := runner.Run("echo test")
	if err == nil {
		t.Error("Run() with nil client expected error, got nil")
	}

	expectedErr := "SSH connection not established"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("Run() error = %q, want %q", err.Error(), expectedErr)
	}
}
