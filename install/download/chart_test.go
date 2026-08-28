package download

import (
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem/osfs"
	"xiaoshiai.cn/installer/install/source"
)

func TestDownloadChartUsesGlobalCacheForExactRepositoryVersion(t *testing.T) {
	chartData := packagedTestChart(t, "demo", "1.2.3")
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(chartData))
	var indexRequests atomic.Int32
	var chartRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.yaml":
			indexRequests.Add(1)
			fmt.Fprintf(writer, "apiVersion: v1\nentries:\n  demo:\n    - name: demo\n      version: 1.2.3\n      digest: %s\n      urls: [demo-1.2.3.tgz]\n", digest)
		case "/demo-1.2.3.tgz":
			chartRequests.Add(1)
			_, _ = writer.Write(chartData)
		default:
			http.NotFound(writer, request)
		}
	}))

	downloader := NewDownloader(t.TempDir(), osfs.New())
	options := DownloadOptions{URL: server.URL, Name: "demo", Version: "1.2.3"}
	first, err := downloader.Download(t.Context(), options)
	if err != nil {
		t.Fatalf("DownloadChart() error = %v", err)
	}
	server.Close()
	second, err := downloader.Download(t.Context(), options)
	if err != nil {
		t.Fatalf("DownloadChart() cache error = %v", err)
	}
	if first.Path != second.Path || indexRequests.Load() != 1 || chartRequests.Load() != 1 {
		t.Fatalf("cache paths/requests = %q %q, index=%d chart=%d", first.Path, second.Path, indexRequests.Load(), chartRequests.Load())
	}
}

func TestIsExactChartVersion(t *testing.T) {
	for _, version := range []string{"1.2.3", "v1.2.3", "=1.2.3", "= 1.2.3", "1.2.3-rc.1+build.1"} {
		if !isExactChartVersion(version) {
			t.Errorf("isExactChartVersion(%q) = false", version)
		}
	}
	for _, version := range []string{"", "1", "1.2", "1.2.x", "^1.2.3", "~1.2.3", ">=1.2.3", "*"} {
		if isExactChartVersion(version) {
			t.Errorf("isExactChartVersion(%q) = true", version)
		}
	}
}

func TestDownloadChartRefreshesRepositoryRangesAndReusesResolvedVersion(t *testing.T) {
	chartData := packagedTestChart(t, "demo", "1.2.3")
	var indexRequests atomic.Int32
	var chartRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.yaml":
			indexRequests.Add(1)
			fmt.Fprint(writer, "apiVersion: v1\nentries:\n  demo:\n    - name: demo\n      version: 1.2.3\n      urls: [demo-1.2.3.tgz]\n")
		case "/demo-1.2.3.tgz":
			chartRequests.Add(1)
			_, _ = writer.Write(chartData)
		}
	}))
	defer server.Close()

	downloader := NewDownloader(t.TempDir(), osfs.New())
	options := DownloadOptions{Type: SourceTypeChart, URL: server.URL, Name: "demo", Version: "^1.0.0"}
	first, err := downloader.Download(t.Context(), options)
	if err != nil {
		t.Fatalf("DownloadChart() error = %v", err)
	}
	second, err := downloader.Download(t.Context(), options)
	if err != nil {
		t.Fatalf("DownloadChart() second error = %v", err)
	}
	if first.Path != second.Path || indexRequests.Load() != 2 || chartRequests.Load() != 1 {
		t.Fatalf("range cache paths/requests = %q %q index=%d chart=%d", first.Path, second.Path, indexRequests.Load(), chartRequests.Load())
	}
}

func TestDownloadChartDoesNotOwnCache(t *testing.T) {
	chartData := packagedTestChart(t, "demo", "1.0.0")
	var indexRequests atomic.Int32
	var chartRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.yaml":
			indexRequests.Add(1)
			fmt.Fprint(writer, "apiVersion: v1\nentries:\n  demo:\n    - name: demo\n      version: 1.0.0\n      urls: [demo-1.0.0.tgz]\n")
		case "/demo-1.0.0.tgz":
			chartRequests.Add(1)
			_, _ = writer.Write(chartData)
		}
	}))
	defer server.Close()

	fsys := osfs.New()
	for _, destination := range []string{
		filepath.Join(t.TempDir(), "first.tgz"),
		filepath.Join(t.TempDir(), "second.tgz"),
	} {
		_, err := DownloadChart(t.Context(), destination, fsys, ChartOptions{
			Repository: server.URL,
			Name:       "demo",
			Version:    "1.0.0",
		})
		if err != nil {
			t.Fatalf("DownloadChart() error = %v", err)
		}
	}
	if indexRequests.Load() != 2 || chartRequests.Load() != 2 {
		t.Fatalf("DownloadChart() requests index=%d chart=%d, want 2 each", indexRequests.Load(), chartRequests.Load())
	}
}

func TestDownloadChartRejectsRepositoryDigestMismatch(t *testing.T) {
	chartData := packagedTestChart(t, "demo", "1.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/index.yaml" {
			fmt.Fprint(writer, "apiVersion: v1\nentries:\n  demo:\n    - name: demo\n      version: 1.0.0\n      digest: sha256:0000\n      urls: [demo-1.0.0.tgz]\n")
			return
		}
		_, _ = writer.Write(chartData)
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "demo.tgz")
	if err := os.WriteFile(destination, []byte("existing chart"), 0o600); err != nil {
		t.Fatalf("write existing chart: %v", err)
	}
	_, err := DownloadChart(t.Context(), destination, osfs.New(), ChartOptions{
		Repository: server.URL,
		Name:       "demo",
		Version:    "1.0.0",
	})
	if err == nil {
		t.Fatal("DownloadChart() accepted a digest mismatch")
	}
	if data, readErr := os.ReadFile(destination); readErr != nil || string(data) != "existing chart" {
		t.Fatalf("existing chart after failed download = %q, %v", data, readErr)
	}
}

func TestDownloadOCIChartResolvesVersionRangeAndUsesCommonTLS(t *testing.T) {
	const username = "chart-user"
	const password = "chart-password"
	registryHandler := registry.New()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actualUsername, actualPassword, ok := request.BasicAuth()
		if !ok || actualUsername != username || actualPassword != password {
			writer.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		registryHandler.ServeHTTP(writer, request)
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()

	caData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	tlsOptions := &install.ResolvedTLS{CAData: caData}
	transport, err := source.NewHTTPTransport(tlsOptions)
	if err != nil {
		t.Fatalf("NewHTTPTransport() error = %v", err)
	}
	host := strings.TrimPrefix(server.URL, "https://")
	for _, version := range []string{"1.0.0", "1.2.0"} {
		pushOCIChart(t, host+"/charts/demo:"+version, packagedTestChart(t, "demo", version), &authn.Basic{Username: username, Password: password}, transport)
	}

	downloaded, err := DownloadChart(t.Context(), filepath.Join(t.TempDir(), "demo.tgz"), osfs.New(), ChartOptions{
		Repository: "oci://" + host + "/charts/demo",
		Name:       "demo",
		Version:    "^1.0.0",
		Auth:       &install.ResolvedAuth{Username: username, Password: password},
		TLS:        tlsOptions,
	})
	if err != nil {
		t.Fatalf("DownloadChart() OCI error = %v", err)
	}
	if downloaded.Version != "1.2.0" {
		t.Fatalf("DownloadChart() OCI version = %q, want 1.2.0", downloaded.Version)
	}
	if _, err := downloaded.FS.Stat(downloaded.Path); err != nil {
		t.Fatalf("cached OCI chart: %v", err)
	}
}

func TestDownloadOCIChartUsesBearerToken(t *testing.T) {
	const token = "chart-token"
	registryHandler := registry.New()
	var server *httptest.Server
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v2/" {
			registryHandler.ServeHTTP(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="`+server.URL+`/token"`)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		registryHandler.ServeHTTP(writer, request)
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	pushOCIChart(t, host+"/charts/demo:1.0.0", packagedTestChart(t, "demo", "1.0.0"), &authn.Bearer{Token: token}, http.DefaultTransport)

	downloaded, err := DownloadChart(t.Context(), filepath.Join(t.TempDir(), "demo.tgz"), osfs.New(), ChartOptions{
		Repository: "oci://" + host + "/charts/demo",
		Name:       "demo",
		Version:    "1.0.0",
		Auth:       &install.ResolvedAuth{Token: token, Username: "ignored", Password: "ignored"},
	})
	if err != nil {
		t.Fatalf("DownloadChart() with bearer token error = %v", err)
	}
	if downloaded.Version != "1.0.0" {
		t.Fatalf("DownloadChart() version = %q, want 1.0.0", downloaded.Version)
	}
}

func pushOCIChart(t *testing.T, reference string, chartData []byte, authenticator authn.Authenticator, transport http.RoundTripper) {
	t.Helper()
	ref, err := name.ParseReference(reference, name.StrictValidation)
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	image := mutate.ConfigMediaType(empty.Image, types.MediaType(ociChartConfigMediaType))
	image, err = mutate.Append(image, mutate.Addendum{
		Layer: static.NewLayer(chartData, types.MediaType(ociChartLayerMediaType)),
	})
	if err != nil {
		t.Fatalf("append chart layer: %v", err)
	}
	if err := remote.Write(ref, image,
		remote.WithTransport(transport),
		remote.WithAuth(authenticator),
	); err != nil {
		t.Fatalf("push OCI chart: %v", err)
	}
}

func packagedTestChart(t *testing.T, name, version string) []byte {
	t.Helper()
	archivePath, err := chartutil.Save(&chart.Chart{Metadata: &chart.Metadata{
		APIVersion: "v2",
		Name:       name,
		Version:    version,
	}}, t.TempDir())
	if err != nil {
		t.Fatalf("save chart: %v", err)
	}
	data, err := os.ReadFile(filepath.Clean(archivePath))
	if err != nil {
		t.Fatalf("read chart: %v", err)
	}
	return data
}
