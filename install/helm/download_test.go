package helm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPGetStopsWhenContextIsCanceled(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := HTTPGet(ctx, server.URL)
		result <- err
	}()
	<-started
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("HTTPGet() returned nil error after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTPGet() did not stop after context cancellation")
	}
}

func TestDownloadStopsAtContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	cacheDir := t.TempDir()
	finished := make(chan error, 1)
	go func() {
		_, _, err := Download(ctx, server.URL, "deadline-test", "0.1.0", cacheDir, "", "")
		finished <- err
	}()

	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("Download() returned nil error after its context deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("Download() did not stop at its context deadline")
	}
}
