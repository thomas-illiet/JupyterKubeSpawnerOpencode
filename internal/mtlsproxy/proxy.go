package mtlsproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// NewHandler returns a fixed-upstream reverse proxy that authenticates to the
// upstream with the supplied client certificate and validates its private CA.
func NewHandler(upstream string, clientCertificate tls.Certificate, caPEM []byte) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil {
		return nil, errors.New("upstream must be an HTTPS URL without embedded credentials")
	}
	if target.RawQuery != "" || target.Fragment != "" {
		return nil, errors.New("upstream must not contain a query or fragment")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("embedded gateway CA bundle contains no certificate")
	}

	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      roots,
			Certificates: []tls.Certificate{clientCertificate},
		},
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Header.Del("X-Forwarded-For")
			request.Out.Header.Del("X-Forwarded-Host")
			request.Out.Header.Del("X-Forwarded-Proto")
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, proxyErr error) {
			http.Error(response, "model gateway unavailable", http.StatusBadGateway)
		},
	}
	return proxy, nil
}

// ListenLoopback refuses wildcard and non-loopback listeners so the embedded
// credential cannot be exercised through a pod-reachable TCP port.
func ListenLoopback(address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !tcpAddress.IP.IsLoopback() {
		_ = listener.Close()
		return nil, fmt.Errorf("listen address %q is not loopback", address)
	}
	return listener, nil
}
