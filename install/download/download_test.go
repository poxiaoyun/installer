package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem/osfs"
)

func TestDownloadCacheBase(t *testing.T) {
	tests := []struct {
		repository string
		want       string
	}{
		{repository: "https://foo.com/bar", want: "/tmp/cache/foo.com/bar"},
		{repository: "https://foo.com:8443/bar", want: "/tmp/cache/foo.com-8443/bar"},
		{repository: "oci://registry.example.com/charts", want: "/tmp/cache/registry.example.com/charts"},
	}
	for _, tt := range tests {
		if got := DownloadCacheBase("/tmp/cache", tt.repository); got != tt.want {
			t.Errorf("DownloadCacheBase(%q) = %q, want %q", tt.repository, got, tt.want)
		}
	}
}

func TestFileSourceUsesURLPath(t *testing.T) {
	location, err := downloadFileSource(osfs.New(), DownloadOptions{
		URL:     "file:///tmp/chart",
		Subpath: "ignored-for-local-source",
	})
	if err != nil {
		t.Fatalf("downloadFileSource() error = %v", err)
	}
	if location.Path != "/tmp/chart" {
		t.Fatalf("downloadFileSource() path = %q", location.Path)
	}
}

func TestTarGzSourceOwnsCachePolicy(t *testing.T) {
	archive := testTgzArchive(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	downloader := NewDownloader(t.TempDir(), osfs.New())
	fixed := DownloadOptions{
		Type:    SourceTypeTarGz,
		URL:     server.URL + "/bundle.tgz",
		Name:    "demo",
		Version: "1.0.0",
	}
	first, err := downloader.Download(t.Context(), fixed)
	if err != nil {
		t.Fatalf("download fixed tar.gz: %v", err)
	}
	second, err := downloader.Download(t.Context(), fixed)
	if err != nil {
		t.Fatalf("reuse fixed tar.gz: %v", err)
	}
	if first.Path != second.Path || requests.Load() != 1 {
		t.Fatalf("fixed tar.gz paths/requests = %q %q/%d", first.Path, second.Path, requests.Load())
	}

	latest := fixed
	latest.Version = ""
	if _, err := downloader.Download(t.Context(), latest); err != nil {
		t.Fatalf("download latest tar.gz: %v", err)
	}
	if _, err := downloader.Download(t.Context(), latest); err != nil {
		t.Fatalf("refresh latest tar.gz: %v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("tar.gz requests = %d, want 3", requests.Load())
	}
}

func TestTarGzSourceDoesNotCacheFailedDownload(t *testing.T) {
	archive := testTgzArchive(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = writer.Write([]byte("invalid archive"))
			return
		}
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	downloader := NewDownloader(cacheDir, osfs.New())
	options := DownloadOptions{
		Type:    SourceTypeTarGz,
		URL:     server.URL + "/bundle.tgz",
		Name:    "demo",
		Version: "1.0.0",
	}
	if _, err := downloader.Download(t.Context(), options); err == nil {
		t.Fatal("invalid tar.gz download succeeded")
	}
	destination := filepath.Join(DownloadCacheBase(cacheDir, options.URL), "demo-1.0.0")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed download cache stat = %v", err)
	}
	if _, err := downloader.Download(t.Context(), options); err != nil {
		t.Fatalf("retry tar.gz download: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("tar.gz requests = %d, want 2", requests.Load())
	}
}

func TestArchiveDownloadsUseSourceTLS(t *testing.T) {
	tests := []struct {
		name     string
		suffix   string
		archive  func(*testing.T) []byte
		download func(DownloadOptions, string) error
	}{
		{name: "zip", suffix: ".zip", archive: testZipArchive, download: func(options DownloadOptions, destination string) error {
			return DownloadZip(t.Context(), destination, osfs.New(), options)
		}},
		{name: "tgz", suffix: ".tgz", archive: testTgzArchive, download: func(options DownloadOptions, destination string) error {
			return DownloadTgz(t.Context(), destination, osfs.New(), options)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := tt.archive(t)
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				username, password, ok := request.BasicAuth()
				if !ok || username != "source-user" || password != "source-password" {
					http.Error(writer, "unauthorized", http.StatusUnauthorized)
					return
				}
				_, _ = writer.Write(archive)
			}))
			defer server.Close()

			options := DownloadOptions{
				URL:  server.URL + "/bundle" + tt.suffix,
				Auth: &install.ResolvedAuth{Username: "source-user", Password: "source-password"},
			}
			destination := t.TempDir()
			if err := tt.download(options, destination); err == nil {
				t.Fatal("download accepted an untrusted TLS certificate by default")
			}

			destination = t.TempDir()
			options.TLS = &install.ResolvedTLS{CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}
			if err := tt.download(options, destination); err != nil {
				t.Fatalf("download with CAData error = %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(destination, "manifest.yaml")); err != nil || string(got) != "kind: ConfigMap\n" {
				t.Fatalf("downloaded manifest = %q, %v", got, err)
			}

			destination = t.TempDir()
			options.TLS = &install.ResolvedTLS{InsecureSkipVerify: true}
			if err := tt.download(options, destination); err != nil {
				t.Fatalf("download with InsecureSkipVerify error = %v", err)
			}
		})
	}
}

func TestGitCloneOptionsUseSourceTLS(t *testing.T) {
	tlsConfig := &install.ResolvedTLS{
		CAData:             []byte("ca"),
		CertData:           []byte("cert"),
		KeyData:            []byte("key"),
		InsecureSkipVerify: true,
	}
	options := gitCloneOptions(DownloadOptions{
		URL:  "https://example.test/repository.git",
		TLS:  tlsConfig,
		Auth: &install.ResolvedAuth{Username: "git-user", Password: "git-password"},
	})
	if !options.InsecureSkipTLS || !bytes.Equal(options.CABundle, tlsConfig.CAData) || !bytes.Equal(options.ClientCert, tlsConfig.CertData) || !bytes.Equal(options.ClientKey, tlsConfig.KeyData) {
		t.Fatalf("gitCloneOptions() TLS fields = %#v", options)
	}
	auth, ok := options.Auth.(*githttp.BasicAuth)
	if !ok || auth.Username != "git-user" || auth.Password != "git-password" {
		t.Fatalf("gitCloneOptions() auth = %#v", options.Auth)
	}

	tokenOptions := gitCloneOptions(DownloadOptions{
		URL:  "https://example.test/repository.git",
		Auth: &install.ResolvedAuth{Token: "git-token", Username: "ignored", Password: "ignored"},
	})
	tokenAuth, ok := tokenOptions.Auth.(*githttp.BasicAuth)
	if !ok || tokenAuth.Username != "oauth2" || tokenAuth.Password != "git-token" {
		t.Fatalf("gitCloneOptions() token auth = %#v", tokenOptions.Auth)
	}
}

func testZipArchive(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	file, err := writer.Create("manifest.yaml")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	_, _ = file.Write([]byte("kind: ConfigMap\n"))
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return output.Bytes()
}

func testTgzArchive(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte("kind: ConfigMap\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "manifest.yaml", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	_, _ = tarWriter.Write(content)
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return output.Bytes()
}
