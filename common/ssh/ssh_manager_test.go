package ssh

import (
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
			if manager.getUser() != tt.user {
				t.Errorf("getUser() = %q, want %q", manager.getUser(), tt.user)
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
			if got := manager.getUser(); got != tt.wantUser {
				t.Errorf("getUser() = %q, want %q", got, tt.wantUser)
			}
		})
	}
}

func TestSshManager_GetPassword(t *testing.T) {
	tests := []struct {
		name         string
		password     string
		wantPassword string
	}{
		{
			name:         "normal password",
			password:     "password123",
			wantPassword: "password123",
		},
		{
			name:         "empty password",
			password:     "",
			wantPassword: "",
		},
		{
			name:         "complex password",
			password:     "P@ssw0rd!#$%^&*()",
			wantPassword: "P@ssw0rd!#$%^&*()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewSshManager("admin", tt.password, "")
			if got := manager.getPassword(); got != tt.wantPassword {
				t.Errorf("getPassword() = %q, want %q", got, tt.wantPassword)
			}
		})
	}
}

func TestSshManager_GetPassphrase(t *testing.T) {
	tests := []struct {
		name           string
		passphrase     string
		wantPassphrase string
	}{
		{
			name:           "normal passphrase",
			passphrase:     "mypassphrase",
			wantPassphrase: "mypassphrase",
		},
		{
			name:           "empty passphrase",
			passphrase:     "",
			wantPassphrase: "",
		},
		{
			name:           "complex passphrase",
			passphrase:     "Pass!@#$%Phrase123",
			wantPassphrase: "Pass!@#$%Phrase123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewSshManager("admin", "", tt.passphrase)
			if got := manager.getPassphrase(); got != tt.wantPassphrase {
				t.Errorf("getPassphrase() = %q, want %q", got, tt.wantPassphrase)
			}
		})
	}
}

func TestSshManager_CredentialsIsolation(t *testing.T) {
	// Test that credentials are not accessible from outside the package
	password := "secret-password-123"
	passphrase := "secret-passphrase-456"

	manager := NewSshManager("admin", password, passphrase)

	// getUser should return the user (non-sensitive)
	if user := manager.getUser(); user != "admin" {
		t.Errorf("getUser() = %q, want %q", user, "admin")
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

func BenchmarkSshManager_GetUser(b *testing.B) {
	manager := NewSshManager("admin", "password", "passphrase")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.getUser()
	}
}

func TestSshManager_String(t *testing.T) {
	tests := []struct {
		name       string
		user       string
		password   string
		passphrase string
		wantUser   string
		wantPass   string
		wantPhrase string
	}{
		{
			name:       "all credentials set",
			user:       "admin",
			password:   "secretpassword123",
			passphrase: "keypassphrase456",
			wantUser:   "admin",
			wantPass:   "yes (hidden)",
			wantPhrase: "yes (hidden)",
		},
		{
			name:       "password only",
			user:       "testuser",
			password:   "secretpass123",
			passphrase: "",
			wantUser:   "testuser",
			wantPass:   "yes (hidden)",
			wantPhrase: "no",
		},
		{
			name:       "passphrase only",
			user:       "keyuser",
			password:   "",
			passphrase: "mykeypass",
			wantUser:   "keyuser",
			wantPass:   "no",
			wantPhrase: "yes (hidden)",
		},
		{
			name:       "no credentials",
			user:       "emptyuser",
			password:   "",
			passphrase: "",
			wantUser:   "emptyuser",
			wantPass:   "no",
			wantPhrase: "no",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewSshManager(tt.user, tt.password, tt.passphrase)
			result := manager.String()

			// Verify the format
			if !strings.HasPrefix(result, "SshManager{") {
				t.Errorf("String() output doesn't start with 'SshManager{': %s", result)
			}

			// Verify user is present
			if !strings.Contains(result, "user:"+tt.wantUser) {
				t.Errorf("String() output doesn't contain 'user:%s': %s", tt.wantUser, result)
			}

			// Verify password status
			if !strings.Contains(result, "password:"+tt.wantPass) {
				t.Errorf("String() output doesn't contain 'password:%s': %s", tt.wantPass, result)
			}

			// Verify passphrase status
			if !strings.Contains(result, "passphrase:"+tt.wantPhrase) {
				t.Errorf("String() output doesn't contain 'passphrase:%s': %s", tt.wantPhrase, result)
			}

			// Critical: verify actual credentials are NOT in the output
			if tt.password != "" && strings.Contains(result, tt.password) {
				t.Errorf("String() leaked password! Output: %s", result)
			}
			if tt.passphrase != "" && strings.Contains(result, tt.passphrase) {
				t.Errorf("String() leaked passphrase! Output: %s", result)
			}
		})
	}
}
