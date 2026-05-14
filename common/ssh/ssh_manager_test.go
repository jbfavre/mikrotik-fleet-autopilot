package ssh

import (
	"strings"
	"testing"

	"jb.favre/mikrotik-fleet-autopilot/common/core"
)

func TestNewSshManager(t *testing.T) {
	password := core.StringPtr("password123")
	passphrase := core.StringPtr("keypass456")

	manager := NewSshManager("admin", password, passphrase)
	if manager == nil {
		t.Fatal("NewSshManager returned nil")
	}
	if manager.getUser() != "admin" {
		t.Fatalf("getUser() = %q, want %q", manager.getUser(), "admin")
	}
	if manager.getPassword() != password {
		t.Fatal("getPassword() did not return the original pointer")
	}
	if manager.getPassphrase() != passphrase {
		t.Fatal("getPassphrase() did not return the original pointer")
	}
}

func TestSshManager_String(t *testing.T) {
	tests := []struct {
		name       string
		password   *string
		passphrase *string
		wantPass   string
		wantPhrase string
	}{
		{
			name:       "nil credentials",
			password:   nil,
			passphrase: nil,
			wantPass:   "not set",
			wantPhrase: "not set",
		},
		{
			name:       "empty credentials",
			password:   core.StringPtr(""),
			passphrase: core.StringPtr(""),
			wantPass:   "empty (hidden)",
			wantPhrase: "empty (hidden)",
		},
		{
			name:       "non-empty credentials",
			password:   core.StringPtr("secretpassword123"),
			passphrase: core.StringPtr("keypassphrase456"),
			wantPass:   "yes (hidden)",
			wantPhrase: "yes (hidden)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewSshManager("admin", tt.password, tt.passphrase)
			result := manager.String()

			if !strings.Contains(result, "user:admin") {
				t.Errorf("String() output doesn't contain user: %s", result)
			}
			if !strings.Contains(result, "password:"+tt.wantPass) {
				t.Errorf("String() output doesn't contain password status %q: %s", tt.wantPass, result)
			}
			if !strings.Contains(result, "passphrase:"+tt.wantPhrase) {
				t.Errorf("String() output doesn't contain passphrase status %q: %s", tt.wantPhrase, result)
			}
			if tt.password != nil && *tt.password != "" && strings.Contains(result, *tt.password) {
				t.Errorf("String() leaked password: %s", result)
			}
			if tt.passphrase != nil && *tt.passphrase != "" && strings.Contains(result, *tt.passphrase) {
				t.Errorf("String() leaked passphrase: %s", result)
			}
		})
	}
}

func TestSshManager_CloneWithPassword(t *testing.T) {
	oldPassword := core.StringPtr("")
	passphrase := core.StringPtr("ssh-key-passphrase")
	newPassword := core.StringPtr("new-password")

	manager := NewSshManager("admin", oldPassword, passphrase)
	cloned := manager.CloneWithPassword(newPassword)

	if cloned == manager {
		t.Fatal("CloneWithPassword() should return a new instance")
	}
	if cloned.getUser() != manager.getUser() {
		t.Fatalf("CloneWithPassword() user = %q, want %q", cloned.getUser(), manager.getUser())
	}
	if cloned.getPassphrase() != manager.getPassphrase() {
		t.Fatal("CloneWithPassword() should preserve passphrase pointer")
	}
	if cloned.getPassword() != newPassword {
		t.Fatal("CloneWithPassword() should replace password pointer")
	}
}

func BenchmarkNewSshManager(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewSshManager("admin", core.StringPtr("password123"), core.StringPtr("passphrase456"))
	}
}

func BenchmarkSshManager_GetUser(b *testing.B) {
	manager := NewSshManager("admin", core.StringPtr("password"), core.StringPtr("passphrase"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.getUser()
	}
}
