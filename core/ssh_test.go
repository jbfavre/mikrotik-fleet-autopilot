package core

import (
	"errors"
	"testing"
)

func TestSshConnection_Close(t *testing.T) {
	tests := []struct {
		name       string
		closeError error
		wantErr    bool
	}{
		{
			name:       "successful close",
			closeError: nil,
			wantErr:    false,
		},
		{
			name:       "close with already closed error",
			closeError: errors.New("use of closed network connection"),
			wantErr:    false, // Should be silently ignored
		},
		{
			name:       "close with connection already closed error",
			closeError: errors.New("connection already closed"),
			wantErr:    false, // Should be silently ignored
		},
		{
			name:       "close with other error",
			closeError: errors.New("network timeout"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't directly test SshConnection.Close without mocking the internal ssh.Client
			// Instead, we test the IsAlreadyClosedError logic which is used by Close
			isIgnored := IsAlreadyClosedError(tt.closeError)
			expectedIgnored := (tt.closeError != nil && !tt.wantErr)

			if isIgnored != expectedIgnored {
				t.Errorf("IsAlreadyClosedError() = %v, want %v for error: %v", isIgnored, expectedIgnored, tt.closeError)
			}
		})
	}
}

func TestIsAlreadyClosedError_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "empty error message",
			err:  errors.New(""),
			want: false,
		},
		{
			name: "case sensitive - lowercase",
			err:  errors.New("USE OF CLOSED NETWORK CONNECTION"),
			want: false, // Function is case-sensitive
		},
		{
			name: "wrapped closed connection error",
			err:  errors.New("failed to read: use of closed network connection"),
			want: true,
		},
		{
			name: "connection already closed with context",
			err:  errors.New("ssh: connection already closed by peer"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAlreadyClosedError(tt.err); got != tt.want {
				t.Errorf("IsAlreadyClosedError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSshRunner_Interface(t *testing.T) {
	// Test that SshConnection implements SshRunner interface
	var _ SshRunner = (*SshConnection)(nil)

	// This test ensures the interface contract is maintained
	t.Log("SshConnection correctly implements SshRunner interface")
}

// TestSshConnectionStructure verifies the SshConnection struct can be created
func TestSshConnectionStructure(t *testing.T) {
	// We can't create a real ssh.Client without connecting to a server,
	// so we just verify the struct definition is correct
	conn := &SshConnection{}

	// Verify struct has expected zero value
	if conn.client != nil {
		t.Error("SshConnection.client should be nil after initialization")
	}
}
