package httpclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type trackedBody struct {
	reader   io.Reader
	readErr  error
	closeErr error
	closed   atomic.Bool
}

func (body *trackedBody) Read(target []byte) (int, error) {
	count, err := body.reader.Read(target)
	if err == io.EOF && body.readErr != nil {
		return count, body.readErr
	}
	return count, err
}

func (body *trackedBody) Close() error {
	body.closed.Store(true)
	return body.closeErr
}

func TestDoPreservesStreamingBodyOwnership(t *testing.T) {
	body := &trackedBody{reader: bytes.NewBufferString("stream")}
	client := newTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	}))
	request := mustRequest(t, "http://example.test/stream")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if body.closed.Load() {
		t.Fatal("Do closed response Body")
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "stream" {
		t.Fatalf("Body=%q error=%v", data, err)
	}
	if err := response.Body.Close(); err != nil || !body.closed.Load() {
		t.Fatalf("Close() error=%v closed=%v", err, body.closed.Load())
	}
}

func TestDoBytesReturnsOwnedSnapshotAndKeepsHTTPStatus(t *testing.T) {
	header := http.Header{"X-Test": {"before"}}
	body := &trackedBody{reader: bytes.NewBufferString("body")}
	client := newTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     header,
			Body:       body,
			Request:    request,
		}, nil
	}))
	response, err := client.DoBytes(mustRequest(t, "http://example.test"))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || response.Header.Get("X-Test") != "before" ||
		string(response.Body) != "body" || !body.closed.Load() {
		t.Fatalf("response=%+v closed=%v", response, body.closed.Load())
	}
	response.Header.Set("X-Test", "changed")
	response.Body[0] = 'x'
	if header.Get("X-Test") != "before" {
		t.Fatal("DoBytes returned the Transport Header map")
	}
}

func TestDoBytesExactLimitAndOverflow(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    string
		wantErr error
	}{
		{name: "below", body: "123"},
		{name: "exact", body: "1234"},
		{name: "over", body: "12345", wantErr: ErrResponseBodyTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracked := &trackedBody{reader: bytes.NewBufferString(test.body)}
			client := newTestClientWithLimit(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: tracked, Request: request}, nil
			}), 4)
			response, err := client.DoBytes(mustRequest(t, "http://example.test"))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("DoBytes() error=%v want=%v", err, test.wantErr)
			}
			if test.wantErr == nil && string(response.Body) != test.body {
				t.Fatalf("Body=%q", response.Body)
			}
			if !tracked.closed.Load() {
				t.Fatal("DoBytes did not close Body")
			}
		})
	}
}

func TestDoBytesJoinsReadLimitAndCloseErrors(t *testing.T) {
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	tests := []struct {
		name    string
		body    *trackedBody
		wantErr error
	}{
		{name: "read and close", body: &trackedBody{
			reader: bytes.NewBufferString("body"), readErr: readErr, closeErr: closeErr,
		}, wantErr: readErr},
		{name: "limit and close", body: &trackedBody{
			reader: bytes.NewBufferString("12345"), closeErr: closeErr,
		}, wantErr: ErrResponseBodyTooLarge},
		{name: "close", body: &trackedBody{
			reader: bytes.NewBufferString("body"), closeErr: closeErr,
		}, wantErr: closeErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClientWithLimit(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: test.body, Request: request}, nil
			}), 4)
			_, err := client.DoBytes(mustRequest(t, "http://example.test"))
			if !errors.Is(err, test.wantErr) ||
				(test.body.closeErr != nil && !errors.Is(err, test.body.closeErr)) {
				t.Fatalf("DoBytes() error=%v", err)
			}
		})
	}
}

func TestClientTimeoutAndRequestCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	options := DefaultOptions()
	options.Timeout = 40 * time.Millisecond
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	if _, err := client.DoBytes(request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("total timeout error=%v", err)
	}

	options.Timeout = time.Second
	client, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	requestContext, cancel := context.WithCancel(context.Background())
	request, _ = http.NewRequestWithContext(requestContext, http.MethodGet, server.URL, nil)
	cancel()
	if _, err := client.DoBytes(request); !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation error=%v", err)
	}
}

func TestRedirectPolicy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	var calls atomic.Int64
	options := DefaultOptions()
	options.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		calls.Add(1)
		if request.URL.String() != target.URL || len(previous) != 1 {
			t.Errorf("redirect request=%s previous=%d", request.URL, len(previous))
		}
		return http.ErrUseLastResponse
	}
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.DoBytes(mustRequest(t, redirect.URL))
	if err != nil || response.StatusCode != http.StatusFound || calls.Load() != 1 {
		t.Fatalf("response=%+v calls=%d error=%v", response, calls.Load(), err)
	}
}

func TestCookieJarPersistsCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/set" {
			http.SetCookie(writer, &http.Cookie{Name: "session", Value: "origin", Path: "/"})
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		cookie, err := request.Cookie("session")
		if err != nil || cookie.Value != "origin" {
			http.Error(writer, "missing cookie", http.StatusUnauthorized)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Jar = jar
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if response, err := client.DoBytes(mustRequest(t, server.URL+"/set")); err != nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("set cookie response=%+v error=%v", response, err)
	}
	if response, err := client.DoBytes(mustRequest(t, server.URL+"/read")); err != nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("read cookie response=%+v error=%v", response, err)
	}
}

func TestResponseHeaderLimitsAndTimeout(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Large", strings.Repeat("x", 1024))
			writer.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		transportOptions := DefaultTransportOptions()
		transportOptions.MaxResponseHeaderBytes = 128
		transport, err := NewTransport(transportOptions)
		if err != nil {
			t.Fatal(err)
		}
		client := newTestClient(t, transport)
		defer client.CloseIdleConnections()
		if _, err := client.DoBytes(mustRequest(t, server.URL)); err == nil {
			t.Fatal("oversized response Header succeeded")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			writer.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		transportOptions := DefaultTransportOptions()
		transportOptions.ResponseHeaderTimeout = 30 * time.Millisecond
		transport, err := NewTransport(transportOptions)
		if err != nil {
			t.Fatal(err)
		}
		client := newTestClient(t, transport)
		defer client.CloseIdleConnections()
		if _, err := client.DoBytes(mustRequest(t, server.URL)); err == nil {
			t.Fatal("response Header timeout succeeded")
		}
	})
}

func TestTransportProxyCallback(t *testing.T) {
	var calls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Host != "origin.invalid" || request.URL.Path != "/resource" {
			http.Error(writer, "unexpected proxy target", http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte("proxied"))
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transportOptions := DefaultTransportOptions()
	transportOptions.Proxy = func(request *http.Request) (*url.URL, error) {
		calls.Add(1)
		if request.URL.Host != "origin.invalid" {
			t.Errorf("Proxy request URL=%s", request.URL)
		}
		return proxyURL, nil
	}
	transport, err := NewTransport(transportOptions)
	if err != nil {
		t.Fatal(err)
	}
	client := newTestClient(t, transport)
	defer client.CloseIdleConnections()
	response, err := client.DoBytes(mustRequest(t, "http://origin.invalid/resource"))
	if err != nil || response.StatusCode != http.StatusOK || string(response.Body) != "proxied" ||
		calls.Load() != 1 {
		t.Fatalf("response=%+v proxy_calls=%d error=%v", response, calls.Load(), err)
	}
}

func TestDefaultTLSVerificationAndCustomRootCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("secure"))
	}))
	defer server.Close()

	client, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DoBytes(mustRequest(t, server.URL)); err == nil {
		t.Fatal("default Client trusted the test self-signed certificate")
	}

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transportOptions := DefaultTransportOptions()
	transportOptions.TLSConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	transport, err := NewTransport(transportOptions)
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Transport = transport
	client, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.DoBytes(mustRequest(t, server.URL))
	if err != nil || string(response.Body) != "secure" {
		t.Fatalf("Body=%q error=%v", response.Body, err)
	}
}

func TestDoBytesLimitAppliesAfterTransparentGzip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(writer)
		_, _ = gzipWriter.Write([]byte("12345"))
		_ = gzipWriter.Close()
	}))
	defer server.Close()
	options := DefaultOptions()
	options.MaxResponseBodySize = 4
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := client.DoBytes(mustRequest(t, server.URL)); !errors.Is(err, ErrResponseBodyTooLarge) {
		t.Fatalf("gzip limit error=%v", err)
	}
}

func TestClientReusesConnectionsAndPrivatePoolsDoNotShare(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	first, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer first.CloseIdleConnections()
	for range 5 {
		if _, err := first.DoBytes(mustRequest(t, server.URL)); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("sequential requests used %d connections", connections.Load())
	}
	second, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer second.CloseIdleConnections()
	if _, err := second.DoBytes(mustRequest(t, server.URL)); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 2 {
		t.Fatalf("private Clients used %d total connections", connections.Load())
	}
}

func TestInjectedClientsShareTransportAndIdleClose(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	transport, err := NewTransport(DefaultTransportOptions())
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Transport = transport
	first, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []*Client{first, second} {
		if _, err := client.DoBytes(mustRequest(t, server.URL)); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("shared Transport used %d connections", connections.Load())
	}
	first.CloseIdleConnections()
	if _, err := second.DoBytes(mustRequest(t, server.URL)); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 2 {
		t.Fatalf("shared idle close left connection count=%d", connections.Load())
	}
	second.CloseIdleConnections()
}

func TestConcurrentRequestsAndCloseIdleConnections(t *testing.T) {
	var connections atomic.Int64
	var active atomic.Int64
	var maximumActive atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			maximum := maximumActive.Load()
			if current <= maximum || maximumActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		_, _ = writer.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	transportOptions := DefaultTransportOptions()
	transportOptions.MaxIdleConns = 4
	transportOptions.MaxIdleConnsPerHost = 2
	transportOptions.MaxConnsPerHost = 2
	transport, err := NewTransport(transportOptions)
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Transport = transport
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsChannel := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, requestErr := client.DoBytes(mustRequest(t, server.URL))
			errorsChannel <- requestErr
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for requestErr := range errorsChannel {
		if requestErr != nil {
			t.Fatal(requestErr)
		}
	}
	if maximumActive.Load() > 2 || connections.Load() > 2 {
		t.Fatalf("maximum active=%d connections=%d", maximumActive.Load(), connections.Load())
	}
	beforeClose := connections.Load()
	client.CloseIdleConnections()
	if _, err := client.DoBytes(mustRequest(t, server.URL)); err != nil {
		t.Fatal(err)
	}
	if connections.Load() <= beforeClose {
		t.Fatal("CloseIdleConnections did not force a later new connection")
	}
	client.CloseIdleConnections()
}

func TestCloseIdleConnectionsDoesNotInterruptActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = writer.Write([]byte("done"))
	}))
	defer server.Close()
	client, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	result := make(chan error, 1)
	request := mustRequest(t, server.URL)
	go func() {
		response, requestErr := client.DoBytes(request)
		if requestErr == nil && string(response.Body) != "done" {
			requestErr = errors.New("unexpected response Body")
		}
		result <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	client.CloseIdleConnections()
	close(release)
	select {
	case requestErr := <-result:
		if requestErr != nil {
			t.Fatal(requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("active request was left blocked")
	}
}

func TestNilClientAndNilRequest(t *testing.T) {
	var client *Client
	if _, err := client.Do(nil); err == nil {
		t.Fatal("nil Client.Do succeeded")
	}
	if _, err := client.DoBytes(nil); err == nil {
		t.Fatal("nil Client.DoBytes succeeded")
	}
	client.CloseIdleConnections()

	client, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := client.Do(nil); err == nil {
		t.Fatal("Do(nil) succeeded")
	}
}

func newTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	return newTestClientWithLimit(t, transport, DefaultOptions().MaxResponseBodySize)
}

func newTestClientWithLimit(t *testing.T, transport http.RoundTripper, maximum int64) *Client {
	t.Helper()
	options := DefaultOptions()
	options.Transport = transport
	options.MaxResponseBodySize = maximum
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func mustRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
