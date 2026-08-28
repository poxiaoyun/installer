package source

import (
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"xiaoshiai.cn/installer/install"
)

func TestHTTPFetcherTLSAndEnvironmentTransport(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("source"))
	}))
	defer server.Close()

	defaultFetcher, err := NewHTTPFetcher(HTTPOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewHTTPFetcher() error = %v", err)
	}
	if _, err := defaultFetcher.Get(t.Context(), server.URL); err == nil {
		t.Fatal("Get() accepted an untrusted certificate by default")
	}

	caData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	fetcher, err := NewHTTPFetcher(HTTPOptions{BaseURL: server.URL, TLS: &install.ResolvedTLS{CAData: caData}})
	if err != nil {
		t.Fatalf("NewHTTPFetcher() with CA error = %v", err)
	}
	buffer, err := fetcher.Get(t.Context(), server.URL)
	if err != nil {
		t.Fatalf("Get() with CA error = %v", err)
	}
	if buffer.String() != "source" {
		t.Fatalf("Get() = %q, want source", buffer.String())
	}
}

func TestHTTPFetcherCredentialsStayOnSourceOrigin(t *testing.T) {
	var sourceAuthorization, redirectedAuthorization string
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		redirectedAuthorization = request.Header.Get("Authorization")
		_, _ = writer.Write([]byte("redirected"))
	}))
	defer target.Close()
	sourceServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sourceAuthorization = request.Header.Get("Authorization")
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer sourceServer.Close()

	fetcher, err := NewHTTPFetcher(HTTPOptions{BaseURL: sourceServer.URL, Username: "user", Password: "password"})
	if err != nil {
		t.Fatalf("NewHTTPFetcher() error = %v", err)
	}
	if _, err := fetcher.Get(t.Context(), sourceServer.URL); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	wantAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
	if sourceAuthorization != wantAuthorization {
		t.Fatalf("source Authorization = %q, want %q", sourceAuthorization, wantAuthorization)
	}
	if redirectedAuthorization != "" {
		t.Fatalf("cross-origin Authorization = %q, want empty", redirectedAuthorization)
	}
}

func TestHTTPFetcherTokenTakesPrecedence(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		_, _ = writer.Write([]byte("source"))
	}))
	defer server.Close()

	fetcher, err := NewHTTPFetcher(HTTPOptions{
		BaseURL:  server.URL,
		Token:    "source-token",
		Username: "ignored-user",
		Password: "ignored-password",
	})
	if err != nil {
		t.Fatalf("NewHTTPFetcher() error = %v", err)
	}
	if _, err := fetcher.Get(t.Context(), server.URL); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if authorization != "Bearer source-token" {
		t.Fatalf("Authorization = %q, want bearer token", authorization)
	}
}

func TestHTTPFetcherPassCredentialsAll(t *testing.T) {
	var authorization string
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		_, _ = writer.Write([]byte("redirected"))
	}))
	defer target.Close()
	sourceServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer sourceServer.Close()

	fetcher, err := NewHTTPFetcher(HTTPOptions{
		BaseURL: sourceServer.URL, Username: "user", Password: "password", PassCredentialsAll: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPFetcher() error = %v", err)
	}
	if _, err := fetcher.Get(t.Context(), sourceServer.URL); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if authorization == "" {
		t.Fatal("PassCredentialsAll did not forward credentials")
	}
}
