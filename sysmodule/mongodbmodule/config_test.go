package mongodbmodule

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestNewValidatesAndRedactsConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config Config
	}{
		{name: "empty uri", config: Config{Database: "game"}},
		{name: "empty database", config: Config{URI: "mongodb://localhost"}},
		{name: "wrong scheme", config: Config{URI: "https://secret:password@example.test", Database: "game"}},
		{name: "insecure tls", config: Config{URI: "mongodb://secret:password@localhost/?tlsInsecure=true", Database: "game"}},
		{name: "invalid tls boolean", config: Config{URI: "mongodb://localhost/?tls=secret-password", Database: "game"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(test.config)
			if !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("New() error = %v, want invalid config", err)
			}
			if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("New() error leaked credential: %v", err)
			}
		})
	}
}

func TestNewRejectsNilAndDuplicateConfiguration(t *testing.T) {
	t.Parallel()
	config := Config{URI: "mongodb://localhost", Database: "game"}
	if _, err := New(config, nil); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("New(nil option) error = %v", err)
	}
	module, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := module.configure(config); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("configure twice error = %v", err)
	}
	if err := (&Module{}).Setup(config); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("unbound Setup error = %v", err)
	}
}

func TestDriverOptionsMergeInOrderAndRejectConnectionIdentity(t *testing.T) {
	t.Parallel()
	first := mongooptions.Client().SetAppName("first").SetMaxPoolSize(10)
	second := mongooptions.Client().SetAppName("second").SetMaxPoolSize(20)
	clientOptions, _, err := buildClientOptions(
		Config{URI: "mongodb://localhost/?minPoolSize=2", Database: "game"},
		[]Option{WithDriverOptions(first, second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if clientOptions.AppName == nil || *clientOptions.AppName != "second" {
		t.Fatalf("AppName = %v, want second", clientOptions.AppName)
	}
	if clientOptions.MaxPoolSize == nil || *clientOptions.MaxPoolSize != 20 {
		t.Fatalf("MaxPoolSize = %v, want 20", clientOptions.MaxPoolSize)
	}
	if clientOptions.MinPoolSize == nil || *clientOptions.MinPoolSize != 2 {
		t.Fatalf("MinPoolSize = %v, want URI value 2", clientOptions.MinPoolSize)
	}
	// New/Setup 后直接改写调用方 Options 的标量指针不能污染 Module 快照。
	*second.MaxPoolSize = 99
	*second.AppName = "changed"
	if *clientOptions.MaxPoolSize != 20 || *clientOptions.AppName != "second" {
		t.Fatalf("Driver option snapshot changed: pool=%d app=%q", *clientOptions.MaxPoolSize, *clientOptions.AppName)
	}

	rejected := []*mongooptions.ClientOptions{
		nil,
		mongooptions.Client().ApplyURI("mongodb://other"),
		mongooptions.Client().SetHosts([]string{"other:27017"}),
		mongooptions.Client().SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}),
	}
	for _, option := range rejected {
		if _, err := New(
			Config{URI: "mongodb://localhost", Database: "game"},
			WithDriverOptions(option),
		); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Fatalf("WithDriverOptions(%v) error = %v", option, err)
		}
	}
	if _, err := New(Config{URI: "mongodb://localhost", Database: "game"}, WithDriverOptions()); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("empty WithDriverOptions error = %v", err)
	}
}

func TestTLSConfigurationConflictAndSnapshot(t *testing.T) {
	t.Parallel()
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "mongo.internal"}
	clientOptions, _, err := buildClientOptions(
		Config{URI: "mongodb://localhost", Database: "game"},
		[]Option{WithTLSConfig(config)},
	)
	if err != nil {
		t.Fatal(err)
	}
	config.ServerName = "changed"
	if got := clientOptions.TLSConfig.ServerName; got != "mongo.internal" {
		t.Fatalf("TLS snapshot ServerName = %q", got)
	}

	rejected := []struct {
		name    string
		config  Config
		options []Option
	}{
		{name: "nil tls", config: Config{URI: "mongodb://localhost", Database: "game"}, options: []Option{WithTLSConfig(nil)}},
		{name: "insecure config", config: Config{URI: "mongodb://localhost", Database: "game"}, options: []Option{WithTLSConfig(&tls.Config{InsecureSkipVerify: true})}}, //nolint:gosec // 刻意构造被拒绝的配置。
		{name: "ca and option", config: Config{URI: "mongodb://localhost", Database: "game", TLSCAFile: "ca.pem"}, options: []Option{WithTLSConfig(&tls.Config{})}},
		{name: "uri material and option", config: Config{URI: "mongodb://localhost/?tlsCAFile=ca.pem", Database: "game"}, options: []Option{WithTLSConfig(&tls.Config{})}},
		{name: "tls false and option", config: Config{URI: "mongodb://localhost/?tls=false", Database: "game"}, options: []Option{WithTLSConfig(&tls.Config{})}},
		{name: "duplicate tls option", config: Config{URI: "mongodb://localhost", Database: "game"}, options: []Option{WithTLSConfig(&tls.Config{}), WithTLSConfig(&tls.Config{})}},
	}
	for _, test := range rejected {
		if _, err := New(test.config, test.options...); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Errorf("%s error = %v", test.name, err)
		}
	}
}

func TestConfigWhitespaceIsNormalizedBeforeStorage(t *testing.T) {
	t.Parallel()
	module, err := New(Config{URI: "  mongodb://localhost  ", Database: "  game  "})
	if err != nil {
		t.Fatal(err)
	}
	if module.config.URI != "mongodb://localhost" || module.config.Database != "game" {
		t.Fatalf("normalized config = %#v", module.config)
	}
}

func TestTLSCAFileLoadsSystemRootsAndRejectsInvalidPEM(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	validPath := filepath.Join(directory, "ca.pem")
	invalidPath := filepath.Join(directory, "invalid.pem")
	writeTestCertificate(t, validPath)
	if err := os.WriteFile(invalidPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	clientOptions, _, err := buildClientOptions(
		Config{URI: "mongodb://localhost", Database: "game", TLSCAFile: validPath}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if clientOptions.TLSConfig == nil || clientOptions.TLSConfig.RootCAs == nil {
		t.Fatal("TLSCAFile did not create RootCAs")
	}
	if _, err := New(Config{URI: "mongodb://localhost", Database: "game", TLSCAFile: invalidPath}); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid PEM error = %v", err)
	}
	if _, err := New(Config{URI: "mongodb://localhost", Database: "game", TLSCAFile: filepath.Join(directory, "missing")}); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("missing CA error = %v", err)
	}
}

func writeTestCertificate(t *testing.T, path string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mongodbmodule test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
