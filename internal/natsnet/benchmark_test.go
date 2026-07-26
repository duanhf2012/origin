package natsnet

import (
	"testing"

	"github.com/nats-io/nats.go"
)

// BenchmarkMessageWrapper 记录入站 Message 轻量值包装本身的分配基线。
func BenchmarkMessageWrapper(b *testing.B) {
	// 固定 1KB payload 模拟典型 RPC 数据；循环只构造只读视图，不复制 Data。
	raw := &nats.Msg{
		Subject: "origin.benchmark",
		Data:    make([]byte, 1024),
	}
	var observed int
	handler := func(message Message) {
		observed += len(message.Data)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		handler(Message{Subject: raw.Subject, Data: raw.Data})
	}
	if observed == 0 {
		b.Fatal("Handler 未读取 payload")
	}
}
