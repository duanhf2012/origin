package natsnet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestDefaultOptions 验证全部公开默认值不会随着官方客户端版本变化而漂移。
func TestDefaultOptions(t *testing.T) {
	t.Parallel()

	// 使用两个 Seed 验证 DefaultOptions 同时复制输入切片。
	urls := []string{"nats://127.0.0.1:4222", "nats://127.0.0.1:4223"}
	options := DefaultOptions("test.node", urls...)
	urls[0] = "nats://changed:4222"

	if options.Name != "test.node" {
		t.Fatalf("Name = %q", options.Name)
	}
	if options.URLs[0] != "nats://127.0.0.1:4222" {
		t.Fatalf("URLs 未复制：%q", options.URLs[0])
	}
	if options.MaxMessageSize != 4*1024*1024 {
		t.Fatalf("MaxMessageSize = %d", options.MaxMessageSize)
	}
	if options.ConnectTimeout != 2*time.Second ||
		options.DefaultOperationTimeout != 15*time.Second ||
		options.DrainTimeout != 30*time.Second {
		t.Fatalf("操作超时默认值不正确：%+v", options)
	}
	if !options.Reconnect.Enabled ||
		options.Reconnect.MaxAttempts != 60 ||
		options.Reconnect.BufferSize != 8*1024*1024 {
		t.Fatalf("Reconnect 默认值不正确：%+v", options.Reconnect)
	}
	if options.Subscription.PendingMessages != 16384 {
		t.Fatalf("Subscription 默认值不正确：%+v", options.Subscription)
	}
}

// TestValidateOptions 覆盖每组配置边界和认证互斥规则。
func TestValidateOptions(t *testing.T) {
	t.Parallel()

	// 每个用例从完整默认值开始，只改变一个字段，保证失败原因唯一。
	tests := []struct {
		name   string
		change func(*Options)
	}{
		{name: "empty name", change: func(o *Options) { o.Name = "" }},
		{name: "empty urls", change: func(o *Options) { o.URLs = nil }},
		{name: "bad url", change: func(o *Options) { o.URLs = []string{"://"} }},
		{name: "unsupported scheme", change: func(o *Options) {
			o.URLs = []string{"http://127.0.0.1:4222"}
		}},
		{name: "mixed tls", change: func(o *Options) {
			o.URLs = []string{"nats://127.0.0.1:4222", "tls://127.0.0.1:4223"}
		}},
		{name: "message size", change: func(o *Options) { o.MaxMessageSize = 0 }},
		{name: "connect timeout", change: func(o *Options) { o.ConnectTimeout = 0 }},
		{name: "operation timeout", change: func(o *Options) {
			o.DefaultOperationTimeout = 0
		}},
		{name: "drain timeout", change: func(o *Options) { o.DrainTimeout = 0 }},
		{name: "ping interval", change: func(o *Options) { o.PingInterval = 0 }},
		{name: "pings outstanding", change: func(o *Options) {
			o.MaxPingsOutstanding = 0
		}},
		{name: "negative reconnect attempts", change: func(o *Options) {
			o.Reconnect.MaxAttempts = -1
		}},
		{name: "zero reconnect wait", change: func(o *Options) {
			o.Reconnect.Wait = 0
		}},
		{name: "negative reconnect jitter", change: func(o *Options) {
			o.Reconnect.Jitter = -time.Second
		}},
		{name: "small reconnect buffer", change: func(o *Options) {
			o.Reconnect.BufferSize = o.MaxMessageSize - 1
		}},
		{name: "invalid disabled reconnect buffer", change: func(o *Options) {
			o.Reconnect.BufferSize = -2
		}},
		{name: "zero pending messages", change: func(o *Options) {
			o.Subscription.PendingMessages = 0
		}},
		{name: "password without username", change: func(o *Options) {
			o.Auth.Password = "secret"
		}},
		{name: "mixed auth", change: func(o *Options) {
			o.Auth.Username = "origin"
			o.Auth.Token = "token"
		}},
		{name: "url and option auth", change: func(o *Options) {
			o.URLs = []string{"nats://origin:secret@127.0.0.1:4222"}
			o.Auth.Username = "origin"
		}},
		{name: "missing credential file", change: func(o *Options) {
			o.Auth.CredentialsFile = filepath.Join(t.TempDir(), "missing.creds")
		}},
		{name: "cert without key", change: func(o *Options) {
			o.TLS.Enabled = true
			o.TLS.CertFile = filepath.Join(t.TempDir(), "client.pem")
		}},
		{name: "tls fields while disabled", change: func(o *Options) {
			o.TLS.ServerName = "localhost"
		}},
	}

	// -1 是 RPC Adapter 禁用断线发送缓冲的唯一合法负值。
	disabled := DefaultOptions("test.node", "nats://127.0.0.1:4222")
	disabled.Reconnect.BufferSize = -1
	if _, err := validateOptions(disabled); err != nil {
		t.Fatalf("Reconnect.BufferSize=-1 被拒绝: %v", err)
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := DefaultOptions("test.node", "nats://127.0.0.1:4222")
			test.change(&options)
			_, err := validateOptions(options)
			if !errors.Is(err, errs.ErrInvalidConfig) {
				t.Fatalf("validateOptions() error = %v", err)
			}
		})
	}
}

// TestValidateOptionsFilesAndTLS 覆盖存在文件、TLS URL 和无效 PEM 内容。
func TestValidateOptionsFilesAndTLS(t *testing.T) {
	t.Parallel()

	// 先建立普通文件，验证路径预检查通过但证书内容仍由 TLS 构造阶段严格拒绝。
	tempDir := t.TempDir()
	caFile := filepath.Join(tempDir, "ca.pem")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	options := DefaultOptions("test.node", "tls://127.0.0.1:4222")
	options.TLS.CAFile = caFile
	tlsEnabled, err := validateOptions(options)
	if err != nil || !tlsEnabled {
		t.Fatalf("validateOptions() = %v, %v", tlsEnabled, err)
	}
	if _, err = buildTLSConfig(options.TLS); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("buildTLSConfig() error = %v", err)
	}
}

// TestValidateSubscriptionOptions 验证零值继承和负数拒绝。
func TestValidateSubscriptionOptions(t *testing.T) {
	t.Parallel()

	// 零值必须完整继承 Connection 默认值。
	defaults := SubscriptionDefaults{PendingMessages: 10}
	resolved, err := validateSubscriptionOptions(defaults, SubscriptionOptions{})
	if err != nil {
		t.Fatalf("validateSubscriptionOptions() error = %v", err)
	}
	if resolved.PendingMessages != 10 {
		t.Fatalf("resolved = %+v", resolved)
	}

	// 负消息数不能借用 nats.go 的无限 Pending 语义。
	if _, err = validateSubscriptionOptions(
		defaults,
		SubscriptionOptions{PendingMessages: -1},
	); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("negative PendingMessages error = %v", err)
	}
}

// TestSafeURLAndRedactedCause 验证日志和错误都不会暴露认证信息。
func TestSafeURLAndRedactedCause(t *testing.T) {
	t.Parallel()

	// URL UserInfo、Query 和 Fragment 必须全部移除。
	rawURL := "nats://origin:secret@127.0.0.1:4222?token=value#fragment"
	if got := safeURL(rawURL); got != "nats://127.0.0.1:4222" {
		t.Fatalf("safeURL() = %q", got)
	}

	options := DefaultOptions("test.node", rawURL)
	options.Auth.Password = "standalone-secret"
	cause := errors.New("dial " + rawURL + " password=standalone-secret")
	redacted := redactCause(cause, options)
	if text := redacted.Error(); text == cause.Error() ||
		containsAny(text, "secret", "token=value") {
		t.Fatalf("错误未正确脱敏：%q", text)
	}
	if !errors.Is(redacted, cause) {
		t.Fatal("脱敏错误没有保留原始错误链")
	}
}

// containsAny 报告文本是否包含任一测试禁用片段。
func containsAny(text string, values ...string) bool {
	// 测试辅助函数只为提高敏感信息断言可读性。
	for _, value := range values {
		if value != "" && len(text) >= len(value) {
			for index := 0; index+len(value) <= len(text); index++ {
				if text[index:index+len(value)] == value {
					return true
				}
			}
		}
	}
	return false
}
