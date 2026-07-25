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

	path := filepath.Join(t.TempDir(), "origin.log")
	config := originlog.DefaultConfig()
	config.Console.Enabled = false
	config.File.Enabled = true
	config.File.Path = path
	config.File.Format = originlog.JSONFormat
	config.File.Retention.Compress = false

	runtime, err := zaplog.New(config)
	if err != nil {
		t.Fatalf("zaplog.New() = %v", err)
	}
	logger := runtime.Logger().With(
		originlog.String("node_id", "game-1"),
		originlog.String("service", "PlayerService"),
	)
	logger.Info("ready", originlog.Int64("player_id", 7))
	if err := runtime.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

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
