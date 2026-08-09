package httpjson

import (
	"net/http"
	"testing"
	"time"
)

func TestNewWithProxyConfiguresExplicitProxy(t *testing.T) {
	client, err := NewWithProxy(time.Second, 1, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", client.httpClient.Transport)
	}
	request, err := http.NewRequest(http.MethodGet, "https://fapi.binance.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := transport.Proxy(request)
	if err != nil {
		t.Fatal(err)
	}
	if proxy.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %s", proxy)
	}
}

func TestNewWithProxyRejectsInvalidURL(t *testing.T) {
	if _, err := NewWithProxy(time.Second, 1, "://bad"); err == nil {
		t.Fatal("expected invalid proxy URL error")
	}
}
