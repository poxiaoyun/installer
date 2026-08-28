package source

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"xiaoshiai.cn/installer/install"
)

// NewHTTPTransport returns a clone of http.DefaultTransport configured for a
// URL source. Cloning preserves environment proxies and the standard connection
// settings without mutating process-wide defaults.
func NewHTTPTransport(options *install.ResolvedTLS) (*http.Transport, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("http.DefaultTransport has type %T, expected *http.Transport", http.DefaultTransport)
	}
	transport := defaultTransport.Clone()
	if options == nil {
		return transport, nil
	}
	if (len(options.CertData) == 0) != (len(options.KeyData) == 0) {
		return nil, fmt.Errorf("source client certificate and key must be provided together")
	}
	if !options.InsecureSkipVerify && len(options.CAData) == 0 && len(options.CertData) == 0 {
		return transport, nil
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	if len(options.CAData) > 0 {
		var roots *x509.CertPool
		if transport.TLSClientConfig.RootCAs != nil {
			roots = transport.TLSClientConfig.RootCAs.Clone()
		} else {
			var err error
			roots, err = x509.SystemCertPool()
			if err != nil {
				return nil, fmt.Errorf("load system CA certificates: %w", err)
			}
		}
		if !roots.AppendCertsFromPEM(options.CAData) {
			return nil, fmt.Errorf("source CA data contains no valid PEM certificates")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	if len(options.CertData) > 0 {
		certificate, err := tls.X509KeyPair(options.CertData, options.KeyData)
		if err != nil {
			return nil, fmt.Errorf("load source client certificate: %w", err)
		}
		transport.TLSClientConfig.Certificates = append(
			append([]tls.Certificate(nil), transport.TLSClientConfig.Certificates...),
			certificate,
		)
	}
	transport.TLSClientConfig.InsecureSkipVerify = options.InsecureSkipVerify
	return transport, nil
}

// NewHTTPClient returns an HTTP client backed by the common source transport.
func NewHTTPClient(options *install.ResolvedTLS) (*http.Client, error) {
	transport, err := NewHTTPTransport(options)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport}, nil
}
