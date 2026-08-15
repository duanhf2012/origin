package network_test

import (
	"testing"

	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestSessionIDUsesStandaloneStringIdentity(t *testing.T) {
	const id network.SessionID = "AQIDBAABAgMEBQYHCAkKCwwNDg8"
	if string(id) != "AQIDBAABAgMEBQYHCAkKCwwNDg8" {
		t.Fatalf("SessionID string conversion = %q", id)
	}
	var zero network.SessionID
	if zero != "" {
		t.Fatalf("zero SessionID = %q", zero)
	}
}
