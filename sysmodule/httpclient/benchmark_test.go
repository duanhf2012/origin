package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkDoBytesReusedClient(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	client, err := New(DefaultOptions())
	if err != nil {
		b.Fatal(err)
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := client.DoBytes(request); err != nil {
			b.Fatal(err)
		}
	}
}
