package redismodule

import (
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/config"
)

func TestNormalizeConfigDefaultsAndModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input Config
		mode  Mode
		pool  int
	}{
		{name: "standalone", input: Config{Addresses: []string{" 127.0.0.1:6379 "}}, mode: ModeStandalone, pool: 10 * runtime.GOMAXPROCS(0)},
		{name: "sentinel", input: Config{Mode: ModeSentinel, Addresses: []string{"127.0.0.1:26379"}, Sentinel: SentinelConfig{MasterName: " game-master "}}, mode: ModeSentinel, pool: 10 * runtime.GOMAXPROCS(0)},
		{name: "cluster", input: Config{Mode: ModeCluster, Addresses: []string{"127.0.0.1:7000"}}, mode: ModeCluster, pool: 5 * runtime.GOMAXPROCS(0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, err := normalizeConfig(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if current.Mode != test.mode || current.Protocol != 3 || current.PoolSize != test.pool {
				t.Fatalf("unexpected defaults: %+v", current)
			}
			if current.MaxActiveConnections != test.pool || current.MaxConcurrentDials != test.pool {
				t.Fatalf("unexpected pool limits: %+v", current)
			}
			if current.DialAttempts != 5 || current.Cluster.MaxRedirects != 3 {
				t.Fatalf("unexpected retry defaults: %+v", current)
			}
		})
	}
}

func TestNormalizeConfigRejectsInvalidCombinations(t *testing.T) {
	t.Parallel()
	valid := Config{Addresses: []string{"127.0.0.1:6379"}}
	tests := []Config{
		{},
		{Addresses: []string{"missing-port"}},
		{Addresses: []string{":6379"}},
		{Addresses: []string{"127.0.0.1:0"}},
		{Addresses: []string{"127.0.0.1:65536"}},
		{Addresses: []string{"127.0.0.1:6379", "127.0.0.1:6379"}},
		{Addresses: []string{"127.0.0.1:6379", "127.0.0.1:6380"}},
		{Mode: "unknown", Addresses: valid.Addresses},
		{Mode: ModeCluster, Addresses: valid.Addresses, Database: 1},
		{Mode: ModeSentinel, Addresses: valid.Addresses},
		{Addresses: valid.Addresses, Sentinel: SentinelConfig{MasterName: "master"}},
		{Addresses: valid.Addresses, Cluster: ClusterConfig{ReadFromReplicas: true}},
		{Mode: ModeCluster, Addresses: valid.Addresses, Cluster: ClusterConfig{RouteByLatency: true}},
		{Mode: ModeCluster, Addresses: valid.Addresses, MaxRetries: 1},
		{Addresses: valid.Addresses, TLSCAFile: "ca.pem"},
		{Addresses: valid.Addresses, Protocol: 1},
		{Addresses: valid.Addresses, Database: -1},
		{Addresses: valid.Addresses, DialAttempts: -1},
		{Addresses: valid.Addresses, PoolSize: 4, MaxConcurrentDials: 5},
		{Addresses: valid.Addresses, PoolSize: 4, MaxActiveConnections: 3},
		{Addresses: valid.Addresses, PoolSize: 4, MaxActiveConnections: 4, MinIdleConnections: 5},
		{Addresses: valid.Addresses, MinRetryBackoff: config.Duration(time.Second), MaxRetryBackoff: config.Duration(time.Millisecond)},
	}
	for index, input := range tests {
		if _, err := normalizeConfig(input); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: expected invalid config, got %v", index, err)
		}
	}
}

func TestBuildUniversalOptionsAndTLSRules(t *testing.T) {
	t.Parallel()
	current, err := normalizeConfig(Config{Mode: ModeCluster, Addresses: []string{"127.0.0.1:7000"}, Username: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	options, err := buildUniversalOptions(current, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !options.IsClusterMode || options.MaxRetries != -1 || options.DialerRetries != current.DialAttempts || !options.ContextTimeoutEnabled {
		t.Fatalf("unexpected driver options: %+v", options)
	}
	if options.Password != "secret" || options.PoolSize != current.PoolSize {
		t.Fatal("driver options lost fields")
	}

	tlsInput := &tls.Config{MinVersion: tls.VersionTLS13}
	tlsConfig := Config{Addresses: []string{"127.0.0.1:6380"}, TLS: true}
	module, err := New(tlsConfig, WithTLSConfig(tlsInput))
	if err != nil {
		t.Fatal(err)
	}
	tlsInput.MinVersion = tls.VersionTLS12
	if module.options.TLSConfig.MinVersion != tls.VersionTLS13 {
		t.Fatal("TLS config was not snapshotted")
	}
	if _, err = New(tlsConfig, WithTLSConfig(&tls.Config{InsecureSkipVerify: true})); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected insecure TLS rejection: %v", err)
	}
	if _, err = New(Config{Addresses: tlsConfig.Addresses}, WithTLSConfig(&tls.Config{})); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected TLS flag rejection: %v", err)
	}
}

func TestDialAttemptsMapsToDriverTotalAttempts(t *testing.T) {
	t.Parallel()
	for _, attempts := range []int{1, 5, 9} {
		current, err := normalizeConfig(Config{Addresses: []string{"127.0.0.1:6379"}, DialAttempts: attempts})
		if err != nil {
			t.Fatal(err)
		}
		options, err := buildUniversalOptions(current, nil)
		if err != nil {
			t.Fatal(err)
		}
		if options.DialerRetries != attempts {
			t.Fatalf("DialAttempts=%d mapped to Driver total attempts %d", attempts, options.DialerRetries)
		}
	}
}

func TestConfigInputSnapshot(t *testing.T) {
	t.Parallel()
	addresses := []string{"127.0.0.1:6379"}
	module, err := New(Config{Addresses: addresses})
	if err != nil {
		t.Fatal(err)
	}
	addresses[0] = "127.0.0.1:9999"
	if module.config.Addresses[0] != "127.0.0.1:6379" || module.options.Addrs[0] != "127.0.0.1:6379" {
		t.Fatal("address input was not snapshotted")
	}
}

func TestLoadTLSConfig(t *testing.T) {
	t.Parallel()
	if current, err := loadTLSConfig(""); err != nil || current.MinVersion != tls.VersionTLS12 {
		t.Fatalf("system TLS: %+v %v", current, err)
	}
	if _, err := loadTLSConfig(filepath.Join(t.TempDir(), "missing.pem")); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing CA: %v", err)
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTLSConfig(invalidPath); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid CA: %v", err)
	}
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	validPath := filepath.Join(t.TempDir(), "valid.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(validPath, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if current, err := loadTLSConfig(validPath); err != nil || current.RootCAs == nil {
		t.Fatalf("valid CA: %+v %v", current, err)
	}
}
