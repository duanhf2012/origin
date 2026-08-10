package pb

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/protocol"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type resolver struct{}

func (resolver) New(id protocol.MessageID) (any, bool) {
	if id != 0x1234 {
		return nil, false
	}
	return &wrapperspb.StringValue{}, true
}

func TestDecodeGoldenBothByteOrders(t *testing.T) {
	for _, test := range []struct {
		name  string
		order network.ByteOrder
		raw   []byte
	}{
		{name: "big", order: network.BigEndian, raw: []byte{0x12, 0x34, 0x0a, 0x02, 'o', 'k'}},
		{name: "little", order: network.LittleEndian, raw: []byte{0x34, 0x12, 0x0a, 0x02, 'o', 'k'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			codec, err := NewCodec(test.order)
			if err != nil {
				t.Fatal(err)
			}
			message, err := codec.Decode(test.raw, resolver{})
			if err != nil {
				t.Fatal(err)
			}
			value, ok := message.Value.(*wrapperspb.StringValue)
			if message.ID != 0x1234 || !ok || value.Value != "ok" {
				t.Fatalf("message=%+v", message)
			}
		})
	}
}

func TestDecodeRejectsInvalidPB(t *testing.T) {
	codec, _ := NewCodec(network.BigEndian)
	for _, raw := range [][]byte{nil, {0, 0}, {0x12, 0x34, 0xff}} {
		if _, err := codec.Decode(raw, resolver{}); !errors.Is(err, errs.ErrTransportProtocol) {
			t.Fatalf("Decode(%x) error=%v", raw, err)
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0x12, 0x34, 0x0a, 0x00})
	codec, _ := NewCodec(network.BigEndian)
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = codec.Decode(raw, resolver{})
	})
}
