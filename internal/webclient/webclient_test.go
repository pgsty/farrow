package webclient

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func proxyFunc(t *testing.T, client *http.Client) uintptr {
	t.Helper()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("client transport = %#v", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("client transport has no proxy function")
	}
	return reflect.ValueOf(transport.Proxy).Pointer()
}

func TestNewHonorsProxyEnvironment(t *testing.T) {
	t.Parallel()
	client := New(42 * time.Second)
	if client.Timeout != 42*time.Second {
		t.Fatalf("timeout = %s", client.Timeout)
	}
	if proxyFunc(t, client) != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("client does not consult the HTTP proxy environment")
	}
}

func TestNewWithRedirectPolicyKeepsProxyAndPolicy(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("refused")
	client := NewWithRedirectPolicy(time.Minute, func(*http.Request, []*http.Request) error { return sentinel })
	if client.Timeout != time.Minute || client.CheckRedirect == nil {
		t.Fatalf("client = %#v", client)
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, sentinel) {
		t.Fatalf("redirect policy not wired: %v", err)
	}
	if proxyFunc(t, client) != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("redirect-policy client does not consult the HTTP proxy environment")
	}
}
