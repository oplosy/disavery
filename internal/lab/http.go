package lab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// NewHTTPClient returns a client that resolves names through res and trusts
// only the CA in caPEM. Keep-alives are off, so every request pays for DNS
// (cached per TTL), TCP and TLS like a new client would: a connection kept
// open to an old address cannot hide a repoint from the measurements.
func NewHTTPClient(res *Resolver, caPEM []byte, timeout time.Duration) (*http.Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("no CA certificate found in PEM data")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if _, err := netip.ParseAddr(host); err == nil {
				return dialer.DialContext(ctx, network, addr)
			}
			addrs, err := res.LookupA(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, a := range addrs {
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
				if err == nil {
					return c, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second,
		DisableKeepAlives:   true,
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}
