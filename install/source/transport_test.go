package source

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"xiaoshiai.cn/installer/install"
)

const proxyHelperEnvironment = "INSTALLER_SOURCE_PROXY_HELPER"

func TestNewHTTPTransportPreservesProxyAndVerifiesTLSByDefault(t *testing.T) {
	defaultTransport := http.DefaultTransport.(*http.Transport)
	transport, err := NewHTTPTransport(nil)
	if err != nil {
		t.Fatalf("NewHTTPTransport() error = %v", err)
	}
	if transport == defaultTransport {
		t.Fatal("NewHTTPTransport() returned http.DefaultTransport without cloning it")
	}
	if transport.Proxy == nil {
		t.Fatal("NewHTTPTransport() did not preserve the default proxy function")
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("NewHTTPTransport() disabled TLS certificate verification by default")
	}
}

func TestNewHTTPClientUsesEnvironmentProxy(t *testing.T) {
	if os.Getenv(proxyHelperEnvironment) == "1" {
		client, err := NewHTTPClient(nil)
		if err != nil {
			t.Fatalf("NewHTTPClient() error = %v", err)
		}
		response, err := client.Get("http://source.invalid/chart.tgz")
		if err != nil {
			t.Fatalf("GET through environment proxy error = %v", err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read proxy response: %v", err)
		}
		if string(body) != "proxied" {
			t.Fatalf("proxy response = %q, want proxied", body)
		}
		return
	}

	requests := make(chan *http.Request, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request
		_, _ = writer.Write([]byte("proxied"))
	}))
	defer proxy.Close()

	command := exec.Command(os.Args[0], "-test.run=^TestNewHTTPClientUsesEnvironmentProxy$")
	command.Env = filteredProxyEnvironment(os.Environ())
	command.Env = append(command.Env,
		proxyHelperEnvironment+"=1",
		"HTTP_PROXY="+proxy.URL,
		"HTTPS_PROXY="+proxy.URL,
		"NO_PROXY=",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("proxy helper failed: %v\n%s", err, output)
	}
	select {
	case request := <-requests:
		if request.URL.Host != "source.invalid" {
			t.Fatalf("proxy request host = %q, want source.invalid", request.URL.Host)
		}
	default:
		t.Fatal("environment proxy did not receive the source request")
	}
}

func filteredProxyEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", proxyHelperEnvironment:
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func TestNewHTTPTransportCanSkipTLSVerification(t *testing.T) {
	defaultTransport := http.DefaultTransport.(*http.Transport)
	transport, err := NewHTTPTransport(&install.ResolvedTLS{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("NewHTTPTransport() error = %v", err)
	}
	if transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("NewHTTPTransport() did not disable TLS certificate verification")
	}
	if defaultTransport.TLSClientConfig != nil && defaultTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("NewHTTPTransport() modified http.DefaultTransport TLS configuration")
	}
}

func TestNewHTTPTransportRejectsInvalidCA(t *testing.T) {
	if _, err := NewHTTPTransport(&install.ResolvedTLS{CAData: []byte("not a PEM certificate")}); err == nil {
		t.Fatal("NewHTTPTransport() accepted invalid CA data")
	}
}

func TestNewHTTPTransportRejectsIncompleteClientCertificate(t *testing.T) {
	if _, err := NewHTTPTransport(&install.ResolvedTLS{CertData: []byte("certificate")}); err == nil {
		t.Fatal("NewHTTPTransport() accepted a client certificate without a key")
	}
}

func TestNewHTTPTransportLoadsClientCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	certificate := server.TLS.Certificates[0]
	certData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
	keyDER, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatalf("marshal test private key: %v", err)
	}
	keyData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	transport, err := NewHTTPTransport(&install.ResolvedTLS{CertData: certData, KeyData: keyData})
	if err != nil {
		t.Fatalf("NewHTTPTransport() error = %v", err)
	}
	if len(transport.TLSClientConfig.Certificates) != 1 {
		t.Fatalf("client certificate count = %d, want 1", len(transport.TLSClientConfig.Certificates))
	}
}
