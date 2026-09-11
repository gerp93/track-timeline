package database

import (
	"strings"
	"testing"
)

func TestNewRoomCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		code, err := NewRoomCode()
		if err != nil {
			t.Fatalf("NewRoomCode: %v", err)
		}
		if len(code) != 4 {
			t.Fatalf("NewRoomCode length = %d, want 4", len(code))
		}
		for _, r := range code {
			if !strings.ContainsRune(roomCodeAlphabet, r) {
				t.Fatalf("NewRoomCode %q contains %c outside alphabet", code, r)
			}
		}
		seen[code] = true
	}
	if len(seen) < 2 {
		t.Fatalf("NewRoomCode produced no variety across 40 draws")
	}
}

func TestNewHostToken(t *testing.T) {
	token, err := NewHostToken()
	if err != nil {
		t.Fatalf("NewHostToken: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("NewHostToken length = %d, want 64 hex chars", len(token))
	}
}

func TestValidateGuestDisplayName(t *testing.T) {
	ok, err := ValidateGuestDisplayName("  Sam  ")
	if err != nil || ok != "Sam" {
		t.Fatalf("ValidateGuestDisplayName(Sam) = %q, %v", ok, err)
	}
	if _, err := ValidateGuestDisplayName(""); err == nil {
		t.Fatal("empty name should error")
	}
	if _, err := ValidateGuestDisplayName(strings.Repeat("a", 25)); err == nil {
		t.Fatal("25-rune name should error")
	}
	if _, err := ValidateGuestDisplayName("bad\nname"); err == nil {
		t.Fatal("control characters should error")
	}
}

func TestGuestUserName(t *testing.T) {
	got := GuestUserName("Sam", "AB12")
	if got != "Sam·ab12" {
		t.Fatalf("GuestUserName = %q, want Sam·ab12", got)
	}
}
