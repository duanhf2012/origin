package logging_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/zaplog"
)

func TestPublicZapRuntimeLifecycle(t *testing.T) {
	t.Parallel()

	// 只通过公开包配置一个 JSON 文件输出，不接触内部实现。
	path := filepath.Join(t.TempDir(), "origin.log")
	config := originlog.DefaultConfig()
	config.Console.Enabled = false
	config.File.Enabled = true
	config.File.Path = path
	config.File.Format = originlog.JSONFormat
	config.File.Retention.Compress = false

	// 创建 Runtime，并使用派生 Logger 写入稳定字段和动态字段。
	runtime, err := zaplog.New(config)
	if err != nil {
		t.Fatalf("zaplog.New() = %v", err)
	}
	logger := runtime.Logger().With(
		originlog.String("node_id", "game-1"),
		originlog.String("service", "PlayerService"),
	)
	logger.Info("ready", originlog.Int64("player_id", 7))
	// Flush 验证顺序屏障，Close 验证完整公开生命周期。
	if err := runtime.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// 最后从真实文件读取结果，逐项验证 JSON 公共外观。
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}
	for _, expected := range []string{
		`"msg":"ready"`,
		`"node_id":"game-1"`,
		`"service":"PlayerService"`,
		`"player_id":7`,
		`"caller":`,
	} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("file does not contain %q: %s", expected, content)
		}
	}
}
