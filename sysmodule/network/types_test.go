package network_test

import (
	"testing"

	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestSessionIDUsesStandaloneStringIdentity(t *testing.T) {
	const id network.SessionID = "00010203-0405-4607-8809-0a0b0c0d0e0f"
	if string(id) != "00010203-0405-4607-8809-0a0b0c0d0e0f" {
		t.Fatalf("SessionID string conversion = %q", id)
	}
	var zero network.SessionID
	if zero != "" {
		t.Fatalf("zero SessionID = %q", zero)
	}
}
