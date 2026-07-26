package natsnet_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
)

// TestNKeySeedAuthentication 验证 NKey Seed 文件由官方客户端读取并完成挑战签名。
func TestNKeySeedAuthentication(t *testing.T) {
	t.Parallel()

	// 生成用户 KeyPair，把公钥配置到 Server，私有 Seed 只写入临时文件。
	keyPair, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("nkeys.CreateUser() error = %v", err)
	}
	defer keyPair.Wipe()
	publicKey, err := keyPair.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey() error = %v", err)
	}
	seed, err := keyPair.Seed()
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	seedFile := filepath.Join(t.TempDir(), "user.seed")
	if err = os.WriteFile(seedFile, seed, 0o600); err != nil {
		t.Fatalf("WriteFile(seed) error = %v", err)
	}
	for index := range seed {
		seed[index] = 'x'
	}

	serverOptions := defaultServerOptions()
	serverOptions.Nkeys = []*server.NkeyUser{{Nkey: publicKey}}
	running := startServer(t, serverOptions)

	options := testOptions("integration.nkey", running.ClientURL())
	options.Auth.NKeySeedFile = seedFile
	conn := connectForTest(t, options, nil)
	defer closeConn(t, conn)

	// 非法 Seed 文件应在建立 socket 前按 InvalidConfig 返回。
	badSeedFile := filepath.Join(t.TempDir(), "bad.seed")
	if err = os.WriteFile(badSeedFile, []byte("invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile(bad seed) error = %v", err)
	}
	bad := options
	bad.Name = "integration.nkey.bad"
	bad.Auth.NKeySeedFile = badSeedFile
	failed, err := natsnet.Connect(context.Background(), bad, nil)
	if failed != nil || !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("非法 NKey Connect() = %v, %v", failed, err)
	}
}
