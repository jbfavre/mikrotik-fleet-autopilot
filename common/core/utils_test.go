package core

import "testing"

func TestStringPtr(t *testing.T) {
	t.Run("non-empty string", func(t *testing.T) {
		p := new("secret")
		if p == nil {
			t.Fatal("StringPtr() returned nil")
		}
		if *p != "secret" {
			t.Fatalf("StringPtr() value = %q, want %q", *p, "secret")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		p := new("")
		if p == nil {
			t.Fatal("StringPtr() returned nil")
		}
		if *p != "" {
			t.Fatalf("StringPtr() value = %q, want empty string", *p)
		}
	})

	t.Run("distinct pointers for separate calls", func(t *testing.T) {
		p1 := new("same")
		p2 := new("same")
		if p1 == p2 {
			t.Fatal("StringPtr() returned the same pointer for separate calls")
		}
	})
}
