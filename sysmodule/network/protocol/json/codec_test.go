package json

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network/protocol"
)

type resolver struct{}

type payload struct {
	Text string `json:"text"`
}

func (resolver) New(id protocol.MessageID) (any, bool) {
	if id != 17 {
		return nil, false
	}
	return &payload{}, true
}

func TestDecodeGolden(t *testing.T) {
	message, err := NewCodec().Decode([]byte(`{"id":17,"data":{"text":"hello"}}`), resolver{})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := message.Value.(*payload)
	if message.ID != 17 || !ok || value.Text != "hello" {
		t.Fatalf("message=%+v", message)
	}
}

func TestDecodeRejectsInvalidEnvelope(t *testing.T) {
	for _, raw := range []string{``, `{`, `{"id":0,"data":{}}`, `{"id":17}`} {
		if _, err := NewCodec().Decode([]byte(raw), resolver{}); !errors.Is(err, errs.ErrTransportProtocol) {
			t.Fatalf("Decode(%q) error=%v", raw, err)
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"id":17,"data":{"text":"ok"}}`))
	f.Add([]byte{0xff, 0x00})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = NewCodec().Decode(raw, resolver{})
	})
}
