// Package webclient builds Farrow's outbound HTTP clients. Every download —
// images, catalogs, socket_vmnet — honors the standard proxy environment
// (HTTP_PROXY, HTTPS_PROXY, ALL_PROXY, NO_PROXY) through an explicit
// transport rather than the mutable process-global default.
package webclient

import (
	"net/http"
	"time"
)

func transport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	cloned := base.Clone()
	cloned.Proxy = http.ProxyFromEnvironment
	return cloned
}

// New returns a proxy-aware client with one overall request timeout.
func New(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: transport()}
}

// NewWithRedirectPolicy is New plus a redirect gate.
func NewWithRedirectPolicy(timeout time.Duration, policy func(*http.Request, []*http.Request) error) *http.Client {
	client := New(timeout)
	client.CheckRedirect = policy
	return client
}
