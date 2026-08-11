package ginmodule

import (
	"crypto/tls"
	"testing"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
)

func TestDefaultServerConfigConvertsToDefaults(t *testing.T) {
	configured := DefaultServerConfig()
	options, err := configured.Options()
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	want := DefaultServerOptions()
	if configured.Address != "0.0.0.0:19093" ||
		options.RequestTimeout != want.RequestTimeout ||
		options.ReadHeaderTimeout != want.ReadHeaderTimeout ||
		options.ReadTimeout != want.ReadTimeout ||
		options.WriteTimeout != want.WriteTimeout ||
		options.IdleTimeout != want.IdleTimeout ||
		options.MaxHeaderBytes != want.MaxHeaderBytes ||
		options.MaxRequestBodySize != want.MaxRequestBodySize ||
		options.MaxSafeResponseBodySize != want.MaxSafeResponseBodySize ||
		options.MaxActiveRequests != want.MaxActiveRequests ||
		len(options.TrustedProxies) != 0 || options.SafeErrorMapper == nil {
		t.Fatalf("defaults mismatch: config=%+v options=%+v", configured, options)
	}
}

func TestServerConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ServerConfig)
	}{
		{name: "address", mutate: func(config *ServerConfig) { config.Address = "missing-port" }},
		{name: "request timeout", mutate: func(config *ServerConfig) { config.RequestTimeout = 0 }},
		{name: "read header timeout", mutate: func(config *ServerConfig) { config.ReadHeaderTimeout = 0 }},
		{name: "read timeout", mutate: func(config *ServerConfig) { config.ReadTimeout = 0 }},
		{name: "write timeout", mutate: func(config *ServerConfig) { config.WriteTimeout = 0 }},
		{name: "write budget", mutate: func(config *ServerConfig) {
			config.WriteTimeout = config.RequestTimeout
		}},
		{name: "idle timeout", mutate: func(config *ServerConfig) { config.IdleTimeout = 0 }},
		{name: "header limit", mutate: func(config *ServerConfig) { config.MaxHeaderBytes = 0 }},
		{name: "request body limit", mutate: func(config *ServerConfig) { config.MaxRequestBodySize = 0 }},
		{name: "safe body limit", mutate: func(config *ServerConfig) { config.MaxSafeResponseBodySize = 0 }},
		{name: "active requests", mutate: func(config *ServerConfig) { config.MaxActiveRequests = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configured := DefaultServerConfig()
			test.mutate(&configured)
			if _, err := configured.Options(); err == nil {
				t.Fatal("Options() accepted invalid config")
			}
		})
	}
}

func TestServerConfigOwnsTrustedProxySlice(t *testing.T) {
	configured := DefaultServerConfig()
	configured.TrustedProxies = []string{"127.0.0.1"}
	options, err := configured.Options()
	if err != nil {
		t.Fatal(err)
	}
	configured.TrustedProxies[0] = "10.0.0.1"
	if options.TrustedProxies[0] != "127.0.0.1" {
		t.Fatalf("Options retained caller slice: %v", options.TrustedProxies)
	}
}

func TestRuntimeOptionsRejectMissingTLSCertificateAndMapper(t *testing.T) {
	options := DefaultServerOptions()
	options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	if err := validateServerOptions(options); err == nil {
		t.Fatal("TLS without certificate was accepted")
	}
	options = DefaultServerOptions()
	options.SafeErrorMapper = nil
	if err := validateServerOptions(options); err == nil {
		t.Fatal("nil SafeErrorMapper was accepted")
	}
}

func TestServerConfigAppliesExplicitValues(t *testing.T) {
	configured := DefaultServerConfig()
	configured.Address = "127.0.0.1:0"
	configured.RequestTimeout = originconfig.Duration(2 * time.Second)
	configured.WriteTimeout = originconfig.Duration(3 * time.Second)
	configured.MaxRequestBodySize = originconfig.ByteSize(128)
	configured.MaxSafeResponseBodySize = originconfig.ByteSize(256)
	configured.MaxActiveRequests = 7
	options, err := configured.Options()
	if err != nil {
		t.Fatal(err)
	}
	if options.RequestTimeout != 2*time.Second || options.WriteTimeout != 3*time.Second ||
		options.MaxRequestBodySize != 128 || options.MaxSafeResponseBodySize != 256 ||
		options.MaxActiveRequests != 7 {
		t.Fatalf("explicit values mismatch: %+v", options)
	}
}
