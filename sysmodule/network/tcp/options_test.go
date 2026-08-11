package tcp

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestOptionsValidation(t *testing.T) {
	handler := network.HandlerFuncs{}
	server := DefaultServerOptions(handler)
	if err := validateServerOptions(server); err != nil {
		t.Fatal(err)
	}
	dial := DefaultDialOptions(handler)
	if err := validateDialOptions(dial); err != nil {
		t.Fatal(err)
	}
	client := DefaultClientOptions(handler)
	if err := validateClientOptions(client); err != nil {
		t.Fatal(err)
	}

	server.Frame.LengthFieldSize = 1
	server.Network.MaxMessageSize = 256
	if err := validateServerOptions(server); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("frame capacity error=%v", err)
	}
	dial.Network.MaxSessions = 2
	if err := validateDialOptions(dial); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("dial sessions error=%v", err)
	}
	dial = DefaultDialOptions(handler)
	dial.DialTimeout = 0
	if err := validateDialOptions(dial); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("dial timeout error=%v", err)
	}
	client.Reconnect.Jitter = 1.1
	if err := validateClientOptions(client); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("jitter error=%v", err)
	}
}
