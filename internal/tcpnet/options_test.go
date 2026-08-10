package tcpnet

import (
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()

	// 构造独立 Pool，确认默认配置只保存引用而不隐式创建第二个 Pool。
	pool := bufferpool.NewPool(bufferpool.Options{})
	options := DefaultConnectionOptions(pool)

	// 逐项锁定已经确认的 M5 默认值，防止后续重构无意改变协议或内存边界。
	if options.Pool != pool {
		t.Fatal("DefaultConnectionOptions 没有保留传入 Pool")
	}
	if options.Frame.LengthFieldSize != 4 {
		t.Fatalf("Frame = %+v，期望四字节大端", options.Frame)
	}
	if options.MaxMessageSize != 4*1024*1024 {
		t.Fatalf("MaxMessageSize = %d", options.MaxMessageSize)
	}
	if options.SendQueueFrames != 4096 {
		t.Fatalf("发送队列默认值 = %d", options.SendQueueFrames)
	}
	if options.ReadTimeout != 0 || options.WriteTimeout != 15*time.Second {
		t.Fatalf("读写超时 = (%s, %s)", options.ReadTimeout, options.WriteTimeout)
	}
	if options.KeepAlive != 30*time.Second {
		t.Fatalf("KeepAlive = %s", options.KeepAlive)
	}

	// Listener 必须复用同一组 Connection 默认值并增加独立连接上限。
	listen := DefaultListenOptions(pool)
	if listen.Connection.Pool != pool || listen.MaxConnections != 4096 {
		t.Fatalf("DefaultListenOptions = %+v", listen)
	}
}

func TestValidateConnectionOptions(t *testing.T) {
	t.Parallel()

	// 使用有效基线，每个子测试只破坏一个字段，以精确覆盖校验分支。
	pool := bufferpool.NewPool(bufferpool.Options{})
	valid := DefaultConnectionOptions(pool)
	tests := []struct {
		name   string
		mutate func(*ConnectionOptions)
	}{
		{name: "nil pool", mutate: func(options *ConnectionOptions) { options.Pool = nil }},
		{
			name: "invalid length field",
			mutate: func(options *ConnectionOptions) {
				options.Frame.LengthFieldSize = 3
			},
		},
		{
			name: "zero max message",
			mutate: func(options *ConnectionOptions) {
				options.MaxMessageSize = 0
			},
		},
		{
			name: "length field overflow",
			mutate: func(options *ConnectionOptions) {
				options.Frame.LengthFieldSize = 1
				options.MaxMessageSize = 256
			},
		},
		{
			name: "zero frame capacity",
			mutate: func(options *ConnectionOptions) {
				options.SendQueueFrames = 0
			},
		},
		{
			name: "negative read timeout",
			mutate: func(options *ConnectionOptions) {
				options.ReadTimeout = -time.Nanosecond
			},
		},
		{
			name: "zero write timeout",
			mutate: func(options *ConnectionOptions) {
				options.WriteTimeout = 0
			},
		},
		{
			name: "negative keep alive",
			mutate: func(options *ConnectionOptions) {
				options.KeepAlive = -time.Nanosecond
			},
		},
	}

	// 每个非法组合必须在资源创建前稳定返回 CodeInvalidConfig。
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := valid
			test.mutate(&options)
			err := validateConnectionOptions(options)
			if !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("validateConnectionOptions error = %v", err)
			}
		})
	}

	// 一字节边界 255、关闭 ReadTimeout 和关闭 KeepAlive 都是合法配置。
	boundary := valid
	boundary.Frame.LengthFieldSize = 1
	boundary.MaxMessageSize = 255
	boundary.ReadTimeout = 0
	boundary.KeepAlive = 0
	if err := validateConnectionOptions(boundary); err != nil {
		t.Fatalf("合法边界配置被拒绝：%v", err)
	}
}

func TestValidateListenOptions(t *testing.T) {
	t.Parallel()

	// Listener 先复用连接校验，再单独拒绝无意义的零连接上限。
	pool := bufferpool.NewPool(bufferpool.Options{})
	options := DefaultListenOptions(pool)
	if err := validateListenOptions(options); err != nil {
		t.Fatalf("默认 Listener 配置无效：%v", err)
	}

	options.MaxConnections = 0
	if err := validateListenOptions(options); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("MaxConnections=0 error = %v", err)
	}
}
