package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"xiaoshiai.cn/installer/install"
)

// HTTPOptions configures access to one URL-backed source.
type HTTPOptions struct {
	BaseURL            string
	Token              string
	Username           string
	Password           string
	PassCredentialsAll bool
	UserAgent          string
	TLS                *install.ResolvedTLS
}

// HTTPFetcher is the project-owned HTTP implementation used by URL sources.
type HTTPFetcher struct {
	client  *http.Client
	baseURL *url.URL
	options HTTPOptions
}

func NewHTTPFetcher(options HTTPOptions) (*HTTPFetcher, error) {
	baseURL, err := url.Parse(options.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse source base URL: %w", err)
	}
	client, err := NewHTTPClient(options.TLS)
	if err != nil {
		return nil, err
	}
	fetcher := &HTTPFetcher{baseURL: baseURL, options: options}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		fetcher.setAuthorization(request)
		return nil
	}
	fetcher.client = client
	return fetcher, nil
}

// Open performs a context-bound GET and returns a successful response body.
// The caller must close the returned body.
func (f *HTTPFetcher) Open(ctx context.Context, href string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, href, nil)
	if err != nil {
		return nil, err
	}
	if f.options.UserAgent != "" {
		request.Header.Set("User-Agent", f.options.UserAgent)
	}
	f.setAuthorization(request)

	response, err := f.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		return nil, fmt.Errorf("failed to fetch %s: %s", href, response.Status)
	}
	return response.Body, nil
}

func (f *HTTPFetcher) Get(ctx context.Context, href string) (*bytes.Buffer, error) {
	body, err := f.Open(ctx, href)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	buffer := bytes.NewBuffer(nil)
	if _, err := io.Copy(buffer, body); err != nil {
		return nil, err
	}
	return buffer, nil
}

func (f *HTTPFetcher) setAuthorization(request *http.Request) {
	request.Header.Del("Authorization")
	if f.options.Token == "" && f.options.Username == "" && f.options.Password == "" {
		return
	}
	if f.options.PassCredentialsAll || sameOrigin(f.baseURL, request.URL) {
		if f.options.Token != "" {
			request.Header.Set("Authorization", "Bearer "+f.options.Token)
			return
		}
		request.SetBasicAuth(f.options.Username, f.options.Password)
	}
}

func sameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && left.Scheme == right.Scheme && left.Host == right.Host
}
