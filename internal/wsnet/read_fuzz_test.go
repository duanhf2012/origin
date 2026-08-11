package wsnet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

func FuzzReadMessageBounded(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("small"))
	f.Add(make([]byte, 256))
	f.Add(make([]byte, 1024))
	f.Fuzz(func(t *testing.T, payload []byte) {
		pool, options := testConnectionOptions(t, BinaryMessage, 1024)
		conn := &Conn{options: options}
		packet, err := conn.readMessage(bytes.NewReader(payload))
		if len(payload) > options.MaxMessageSize {
			if !errors.Is(err, errs.ErrTransportMessageTooLarge) || packet != nil {
				t.Fatalf("oversize len=%d packet=%v err=%v", len(payload), packet, err)
			}
			assertPoolEmpty(t, pool)
			return
		}
		if err != nil {
			t.Fatalf("len=%d err=%v", len(payload), err)
		}
		if !bytes.Equal(packet.Bytes(), payload) {
			t.Fatalf("len=%d payload mismatch", len(payload))
		}
		packet.Release()
		assertPoolEmpty(t, pool)
	})
}
