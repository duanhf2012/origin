package kcpnet

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/lengthframe"
)

func BenchmarkKCPRoundTrip(b *testing.B) {
	for _, size := range []int{32, 256, 4 * 1024, 64 * 1024} {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			serverPool, serverOptions := testConnectionOptions(b, 64*1024, lengthframe.BigEndian)
			clientPool, clientOptions := testConnectionOptions(b, 64*1024, lengthframe.BigEndian)
			serverHandler := newRecordingHandler()
			serverHandler.onMessage = func(conn *Conn, packet *bufferpool.Buffer) error {
				if err := conn.Send(packet); err != nil {
					packet.Release()
					return err
				}
				return nil
			}
			listener, err := Listen("127.0.0.1:0", ListenOptions{
				MaxConnections: 1,
				Connection:     serverOptions,
			}, serverHandler)
			if err != nil {
				b.Fatal(err)
			}
			ack := make(chan struct{}, 1)
			clientHandler := newRecordingHandler()
			clientHandler.onMessage = func(_ *Conn, packet *bufferpool.Buffer) error {
				packet.Release()
				ack <- struct{}{}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			client, err := Dial(ctx, listener.Addr().String(), DialOptions{
				Connection: clientOptions,
			}, clientHandler)
			if err != nil {
				cancel()
				_ = listener.Close(context.Background())
				b.Fatal(err)
			}
			payload := make([]byte, size)
			b.ReportAllocs()
			b.SetBytes(int64(size * 2))
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				packet := clientPool.Acquire(size)
				copy(packet.Bytes(), payload)
				if err := client.Send(packet); err != nil {
					packet.Release()
					b.Fatal(err)
				}
				select {
				case <-ack:
				case <-ctx.Done():
					b.Fatal(ctx.Err())
				}
			}
			b.StopTimer()
			client.Close()
			_ = client.Wait(context.Background())
			_ = listener.Close(context.Background())
			cancel()
			assertPoolEmpty(b, serverPool)
			assertPoolEmpty(b, clientPool)
		})
	}
}
