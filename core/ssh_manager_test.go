package core

import (
	"context"
	"strings"
	"testing"
)

func TestNewSshManager(t *testing.T) {
	tests := []struct {
		name       string
		user       string
		password   string
		passphrase string
	}{
		{
			name:       "all credentials provided",
			user:       "admin",
			password:   "password123",
			passphrase: "keypass456",
		},
		{
			name:       "password only",
			user:       "admin",
			password:   "password123",
			passphrase: "",
		},
		{
			name:       "passphrase only",
			user:       "admin",
			password:   "",
			passphrase: "keypass456",
		},
		{
			name:       "no credentials",
			user:       "admin",
			password:   "",
			passphrase: "",
		},
		{
			name:       "empty user",
			user:       "",
			password:   "password123",
			passphrase: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewSshManager(tt.user, tt.password, tt.passphrase)

			if manager == nil {
				t.Fatal("NewSshManager returned nil")
			}

			// Verify user is accessible (non-sensitive)
			if manager.GetUser() != tt.user {
				t.Errorf("GetUser() = %q, want %q", manager.GetUser(), tt.user)
			}

			// Verify credentials are stored (but not accessible directly)
			if manager.user != tt.user {
				t.Errorf("internal user field = %q, want %q", manager.user, tt.user)
			}
			if manager.password != tt.password {
				t.Errorf("internal password field = %q, want %q", manager.password, tt.password)
			}
			if manager.passphrase != tt.passphrase {
				t.Errorf("internal passphrase field = %q, want %q", manager.passphrase, tt.passphrase)
			}
		})
	}
}

func TestSshManager_GetUser(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		wantUser string
	}{
		{
			name:     "normal username",
			user:     "admin",
			wantUser: "admin",
		},
		{
			name:     "email as username",
			user:     "user@example.com",
			wantUser: "user@example.com",
		},
		{
			name:     "empty username",
			user:     "",
			wantUser: "",
		},
		{
			name:     "username with special chars",
			user:     "user-name_123",
			wantUser: "user-name_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewSshManager(tt.user, "password", "passphrase")
			if got := manager.GetUser(); got != tt.wantUser {
				t.Errorf("GetUser() = %q, want %q", got, tt.wantUser)
			}
		})
	}
}

func TestSshManager_CreateConnection_NoAuth(t *testing.T) {
	// Test that CreateConnection fails when no authentication is provided
	manager := NewSshManager("admin", "", "")
	ctx := context.WithValue(context.Background(), SshManagerKey, manager)

	_, err := CreateConnection(ctx, "invalid-host:22")
	if err == nil {
		t.Error("CreateConnection() expected error for no authentication, got nil")
	}

	// The error should mention authentication
	if err != nil && !strings.Contains(err.Error(), "no authentication method provided") {
		t.Errorf("CreateConnection() error should mention authentication, got: %v", err)
	}
}

func TestSshManager_CreateConnection_InvalidHost(t *testing.T) {
	// Test connection failure with invalid host
	manager := NewSshManager("admin", "password123", "")
	ctx := context.WithValue(context.Background(), SshManagerKey, manager)

	// Use an invalid host that will fail to connect
	_, err := CreateConnection(ctx, "invalid.host.that.does.not.exist:22")
	if err == nil {
		t.Error("CreateConnection() expected error for invalid host, got nil")
	}

	// Should contain "failed to dial" in error message
	if err != nil && !strings.Contains(err.Error(), "failed to dial") && !strings.Contains(err.Error(), "failed to create SSH connection") {
		t.Errorf("CreateConnection() expected connection error, got: %v", err)
	}
}

func TestGetSshManager(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		wantErr     bool
		errContains string
	}{
		{
			name: "valid ssh manager in context",
			ctx: context.WithValue(context.Background(), SshManagerKey,
				NewSshManager("admin", "password", "passphrase")),
			wantErr: false,
		},
		{
			name:        "no ssh manager in context",
			ctx:         context.Background(),
			wantErr:     true,
			errContains: "invalid ssh manager type or not found",
		},
		{
			name:        "wrong type in context",
			ctx:         context.WithValue(context.Background(), SshManagerKey, "not a manager"),
			wantErr:     true,
			errContains: "invalid ssh manager type or not found",
		},
		{
			name: "nil value in context",
			ctx:  context.WithValue(context.Background(), SshManagerKey, (*SshManager)(nil)),
			// When a nil pointer of the correct type is stored in context,
			// the type assertion succeeds and returns the nil pointer
			// This is actually a valid case - we get nil manager, no error
			// The caller should check for nil manager
			wantErr: false,
		},
		{
			name: "wrong context key",
			ctx: context.WithValue(context.Background(), ContextKey("wrong-key"),
				NewSshManager("admin", "password", "passphrase")),
			wantErr:     true,
			errContains: "invalid ssh manager type or not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := GetSshManager(tt.ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetSshManager() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if err == nil {
					t.Error("GetSshManager() expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("GetSshManager() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			// For non-error cases, manager can be nil if that's what was stored
			// (e.g., nil pointer of correct type)
			// Most tests expect non-nil manager, but nil is technically valid
			if tt.name != "nil value in context" && manager == nil {
				t.Error("GetSshManager() returned nil manager with no error")
			}
		})
	}
}

func TestSshManager_CredentialsIsolation(t *testing.T) {
	// Test that credentials are not accessible from outside the package
	password := "secret-password-123"
	passphrase := "secret-passphrase-456"

	manager := NewSshManager("admin", password, passphrase)

	// GetUser should return the user (non-sensitive)
	if user := manager.GetUser(); user != "admin" {
		t.Errorf("GetUser() = %q, want %q", user, "admin")
	}

	// Verify we cannot access password or passphrase through public API
	// (This is a design verification test)
	// The only way to use credentials is through CreateConnection()
	if manager == nil {
		t.Error("manager should not be nil")
	}

	// Credentials should be private fields
	// We can only verify this through the fact that they're not exported
	// This test documents the security design
}

func BenchmarkNewSshManager(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewSshManager("admin", "password123", "passphrase456")
	}
}

func BenchmarkGetSshManager(b *testing.B) {
	ctx := context.WithValue(context.Background(), SshManagerKey,
		NewSshManager("admin", "password", "passphrase"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GetSshManager(ctx)
	}
}

func BenchmarkSshManager_GetUser(b *testing.B) {
	manager := NewSshManager("admin", "password", "passphrase")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetUser()
	}
}
