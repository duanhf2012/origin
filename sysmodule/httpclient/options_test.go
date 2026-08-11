package httpclient

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func TestDefaultOptions(t *testing.T) {
	options := DefaultOptions()
	if options.Timeout != 30*time.Second || options.MaxResponseBodySize != 4<<20 ||
		options.Transport != nil || options.CheckRedirect != nil || options.Jar != nil {
		t.Fatalf("DefaultOptions() = %+v", options)
	}
}

func TestDefaultTransportOptions(t *testing.T) {
	options := DefaultTransportOptions()
	if options.DialTimeout != 5*time.Second || options.DialKeepAlive != 30*time.Second ||
		options.TLSHandshakeTimeout != 10*time.Second ||
		options.ResponseHeaderTimeout != 15*time.Second ||
		options.IdleConnTimeout != 90*time.Second || options.MaxIdleConns != 128 ||
		options.MaxIdleConnsPerHost != 16 || options.MaxConnsPerHost != 64 ||
		options.MaxResponseHeaderBytes != 1<<20 || options.Proxy == nil || options.TLSConfig != nil {
		t.Fatalf("DefaultTransportOptions() = %+v", options)
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Options)
	}{
		{name: "timeout", mutate: func(options *Options) { options.Timeout = 0 }},
		{name: "body limit", mutate: func(options *Options) { options.MaxResponseBodySize = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultOptions()
			test.mutate(&options)
			if _, err := New(options); err == nil {
				t.Fatal("New() accepted invalid Options")
			}
		})
	}
}

func TestNewTransportRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TransportOptions)
	}{
		{name: "dial timeout", mutate: func(options *TransportOptions) { options.DialTimeout = 0 }},
		{name: "dial keep alive", mutate: func(options *TransportOptions) { options.DialKeepAlive = 0 }},
		{name: "TLS timeout", mutate: func(options *TransportOptions) { options.TLSHandshakeTimeout = 0 }},
		{name: "header timeout", mutate: func(options *TransportOptions) { options.ResponseHeaderTimeout = 0 }},
		{name: "idle timeout", mutate: func(options *TransportOptions) { options.IdleConnTimeout = 0 }},
		{name: "idle total", mutate: func(options *TransportOptions) { options.MaxIdleConns = 0 }},
		{name: "idle per host", mutate: func(options *TransportOptions) { options.MaxIdleConnsPerHost = 0 }},
		{name: "connections per host", mutate: func(options *TransportOptions) { options.MaxConnsPerHost = 0 }},
		{name: "header bytes", mutate: func(options *TransportOptions) { options.MaxResponseHeaderBytes = 0 }},
		{name: "idle relationship", mutate: func(options *TransportOptions) {
			options.MaxIdleConns = options.MaxIdleConnsPerHost - 1
		}},
		{name: "connection relationship", mutate: func(options *TransportOptions) {
			options.MaxConnsPerHost = options.MaxIdleConnsPerHost - 1
		}},
		{name: "insecure TLS", mutate: func(options *TransportOptions) {
			options.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec -- rejection test
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultTransportOptions()
			test.mutate(&options)
			if _, err := NewTransport(options); err == nil {
				t.Fatal("NewTransport() accepted invalid options")
			}
		})
	}
}

func TestNewTransportAppliesAndOwnsOptions(t *testing.T) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	options := DefaultTransportOptions()
	options.Proxy = nil
	options.TLSConfig = tlsConfig
	options.MaxIdleConns = 32
	options.MaxIdleConnsPerHost = 8
	options.MaxConnsPerHost = 16
	transport, err := NewTransport(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	tlsConfig.MinVersion = tls.VersionTLS12
	if transport.Proxy != nil || transport.TLSClientConfig == tlsConfig ||
		transport.TLSClientConfig.MinVersion != tls.VersionTLS13 ||
		transport.MaxIdleConns != 32 || transport.MaxIdleConnsPerHost != 8 ||
		transport.MaxConnsPerHost != 16 || !transport.ForceAttemptHTTP2 ||
		transport.ExpectContinueTimeout != time.Second || transport.DisableCompression {
		t.Fatalf("transport does not own/apply options: %+v", transport)
	}
}

func TestNewCreatesPrivateTransportOrUsesInjectedTransport(t *testing.T) {
	first, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if first.client.Transport == second.client.Transport {
		t.Fatal("default Clients shared a Transport")
	}
	first.CloseIdleConnections()
	second.CloseIdleConnections()

	injected := &http.Transport{}
	t.Cleanup(injected.CloseIdleConnections)
	options := DefaultOptions()
	options.Transport = injected
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if client.client.Transport != injected {
		t.Fatal("New() replaced injected Transport")
	}
}
