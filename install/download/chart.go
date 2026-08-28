package download

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"sigs.k8s.io/yaml"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem"
	"xiaoshiai.cn/installer/install/source"
)

const (
	maxChartDownloadSize         = 256 << 20
	maxRepositoryIndexSize       = 16 << 20
	ociChartConfigMediaType      = "application/vnd.cncf.helm.config.v1+json"
	ociChartLayerMediaType       = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	ociLegacyChartLayerMediaType = "application/tar+gzip"
)

// ChartOptions describes one chart download.
type ChartOptions struct {
	Repository string
	Name       string
	Version    string
	Auth       *install.ResolvedAuth
	TLS        *install.ResolvedTLS
}

// DownloadedChart identifies the downloaded chart.
type DownloadedChart struct {
	FS      filesystem.FS
	Path    string
	Version string
}

type resolvedChart struct {
	Version string
	Digest  string
	Open    func(context.Context) (io.ReadCloser, error)
}

type repositoryIndex struct {
	APIVersion string                              `json:"apiVersion"`
	Entries    map[string][]repositoryChartVersion `json:"entries"`
}

type repositoryChartVersion struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	URLs       []string `json:"urls"`
	Digest     string   `json:"digest,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
}

// DownloadChart resolves and downloads an HTTP repository or OCI chart without
// using Helm's downloader, getter, registry, repository, or environment APIs.
func DownloadChart(ctx context.Context, destination string, fsys filesystem.WriteFS, options ChartOptions) (*DownloadedChart, error) {
	resolved, err := resolveChart(ctx, options)
	if err != nil {
		return nil, err
	}
	return downloadResolvedChart(ctx, destination, fsys, resolved)
}

func downloadResolvedChart(ctx context.Context, destination string, fsys filesystem.WriteFS, resolved *resolvedChart) (*DownloadedChart, error) {
	body, err := resolved.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	err = atomicDownloadFile(fsys, destination, func(file filesystem.File) error {
		var checksum hash.Hash
		writer := io.Writer(file)
		if resolved.Digest != "" {
			checksum = sha256.New()
			writer = io.MultiWriter(file, checksum)
		}
		written, err := io.Copy(writer, io.LimitReader(body, maxChartDownloadSize+1))
		if err != nil {
			return err
		}
		if written > maxChartDownloadSize {
			return fmt.Errorf("chart exceeds maximum download size of %d bytes", maxChartDownloadSize)
		}
		if checksum != nil {
			actual := fmt.Sprintf("sha256:%x", checksum.Sum(nil))
			if !equalDigest(actual, resolved.Digest) {
				return fmt.Errorf("downloaded chart digest mismatch: expected %s, actual %s", resolved.Digest, actual)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &DownloadedChart{
		FS:      fsys,
		Path:    destination,
		Version: resolved.Version,
	}, nil
}

func resolveChart(ctx context.Context, options ChartOptions) (*resolvedChart, error) {
	if strings.HasPrefix(options.Repository, "oci://") {
		return resolveOCIChart(ctx, options)
	}
	return resolveRepositoryChart(ctx, options)
}

func resolveRepositoryChart(ctx context.Context, options ChartOptions) (*resolvedChart, error) {
	if options.Name == "" {
		return nil, fmt.Errorf("chart name is required for repository %s", options.Repository)
	}
	fetcher, err := newChartHTTPFetcher(options)
	if err != nil {
		return nil, err
	}
	index, err := fetchRepositoryIndex(ctx, fetcher, options.Repository)
	if err != nil {
		return nil, err
	}
	selected, err := selectRepositoryChartVersion(index, options.Name, options.Version)
	if err != nil {
		return nil, err
	}
	chartURL, err := resolveRepositoryURL(options.Repository, selected.URLs[0])
	if err != nil {
		return nil, err
	}
	return &resolvedChart{
		Version: selected.Version,
		Digest:  selected.Digest,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			return fetcher.Open(ctx, chartURL)
		},
	}, nil
}

func newChartHTTPFetcher(options ChartOptions) (*source.HTTPFetcher, error) {
	httpOptions := source.HTTPOptions{
		BaseURL:   options.Repository,
		UserAgent: installerUserAgent(),
		TLS:       options.TLS,
	}
	if options.Auth != nil {
		httpOptions.Token = options.Auth.Token
		httpOptions.Username = options.Auth.Username
		httpOptions.Password = options.Auth.Password
	}
	return source.NewHTTPFetcher(httpOptions)
}

func fetchRepositoryIndex(ctx context.Context, fetcher *source.HTTPFetcher, repository string) (*repositoryIndex, error) {
	indexURL, err := resolveRepositoryURL(repository, "index.yaml")
	if err != nil {
		return nil, err
	}
	body, err := fetcher.Open(ctx, indexURL)
	if err != nil {
		return nil, fmt.Errorf("download chart repository index: %w", err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxRepositoryIndexSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRepositoryIndexSize {
		return nil, fmt.Errorf("chart repository index exceeds %d bytes", maxRepositoryIndexSize)
	}
	index := &repositoryIndex{}
	if err := yaml.Unmarshal(data, index); err != nil {
		return nil, fmt.Errorf("parse chart repository index: %w", err)
	}
	if index.APIVersion == "" || index.Entries == nil {
		return nil, fmt.Errorf("chart repository index is missing apiVersion or entries")
	}
	return index, nil
}

func selectRepositoryChartVersion(index *repositoryIndex, name, version string) (*repositoryChartVersion, error) {
	versions := index.Entries[name]
	if len(versions) == 0 {
		return nil, fmt.Errorf("chart %s not found in repository index", name)
	}
	constraintText := version
	if constraintText == "" {
		constraintText = "*"
	}
	constraint, err := semver.NewConstraint(constraintText)
	if err != nil {
		return nil, fmt.Errorf("invalid chart version constraint %q: %w", version, err)
	}
	type candidate struct {
		version *semver.Version
		entry   *repositoryChartVersion
	}
	candidates := make([]candidate, 0, len(versions))
	for index := range versions {
		entry := &versions[index]
		if entry.Name != "" && entry.Name != name || len(entry.URLs) == 0 {
			continue
		}
		parsed, err := semver.NewVersion(entry.Version)
		if err != nil || !constraint.Check(parsed) {
			continue
		}
		candidates = append(candidates, candidate{version: parsed, entry: entry})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("chart %s has no version matching %q", name, version)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].version.GreaterThan(candidates[j].version)
	})
	return candidates[0].entry, nil
}

func resolveRepositoryURL(repository, reference string) (string, error) {
	base, err := url.Parse(strings.TrimRight(repository, "/") + "/")
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func resolveOCIChart(ctx context.Context, options ChartOptions) (*resolvedChart, error) {
	reference := strings.TrimPrefix(options.Repository, "oci://")
	remoteOptions, err := ociRemoteOptions(ctx, options)
	if err != nil {
		return nil, err
	}
	parsed, resolvedVersion, err := resolveOCIChartReference(reference, options.Version, remoteOptions)
	if err != nil {
		return nil, err
	}
	descriptor, err := remote.Get(parsed, remoteOptions...)
	if err != nil {
		return nil, fmt.Errorf("resolve OCI chart %q: %w", options.Repository, err)
	}
	manifest := &v1.Manifest{}
	if err := json.Unmarshal(descriptor.Manifest, manifest); err != nil {
		return nil, fmt.Errorf("decode OCI chart manifest: %w", err)
	}
	if string(manifest.Config.MediaType) != ociChartConfigMediaType {
		return nil, fmt.Errorf("OCI chart config media type is %q, want %q", manifest.Config.MediaType, ociChartConfigMediaType)
	}
	layerDescriptor, err := findOCIChartLayer(manifest.Layers)
	if err != nil {
		return nil, err
	}
	layerDigest := layerDescriptor.Digest.String()
	return &resolvedChart{
		Version: resolvedVersion,
		Digest:  layerDigest,
		Open: func(context.Context) (io.ReadCloser, error) {
			layerRef := parsed.Context().Digest(layerDigest)
			layer, err := remote.Layer(layerRef, remoteOptions...)
			if err != nil {
				return nil, fmt.Errorf("download OCI chart layer %s: %w", layerDigest, err)
			}
			return layer.Compressed()
		},
	}, nil
}

func resolveOCIChartReference(reference, version string, options []remote.Option) (name.Reference, string, error) {
	lastSegment := reference[strings.LastIndex(reference, "/")+1:]
	if strings.Contains(reference, "@") || strings.Contains(lastSegment, ":") {
		parsed, err := name.ParseReference(reference, name.StrictValidation)
		return parsed, version, err
	}
	if isExactChartVersion(version) {
		parsed, err := name.ParseReference(reference+":"+strings.ReplaceAll(version, "+", "_"), name.StrictValidation)
		return parsed, version, err
	}
	repository, err := name.NewRepository(reference, name.StrictValidation)
	if err != nil {
		return nil, "", err
	}
	tags, err := remote.List(repository, options...)
	if err != nil {
		return nil, "", fmt.Errorf("list OCI chart tags: %w", err)
	}
	constraintText := version
	if constraintText == "" {
		constraintText = "*"
	}
	constraint, err := semver.NewConstraint(constraintText)
	if err != nil {
		return nil, "", err
	}
	type candidate struct {
		tag     string
		version *semver.Version
	}
	candidates := []candidate{}
	for _, tag := range tags {
		parsed, err := semver.NewVersion(strings.ReplaceAll(tag, "_", "+"))
		if err == nil && constraint.Check(parsed) {
			candidates = append(candidates, candidate{tag: tag, version: parsed})
		}
	}
	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("OCI chart has no version matching %q", version)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].version.GreaterThan(candidates[j].version)
	})
	selected := candidates[0]
	parsed, err := name.ParseReference(reference+":"+selected.tag, name.StrictValidation)
	return parsed, selected.version.Original(), err
}

func ociRemoteOptions(ctx context.Context, options ChartOptions) ([]remote.Option, error) {
	transport, err := source.NewHTTPTransport(options.TLS)
	if err != nil {
		return nil, err
	}
	remoteOptions := []remote.Option{
		remote.WithContext(ctx),
		remote.WithTransport(transport),
		remote.WithUserAgent(installerUserAgent()),
	}
	if options.Auth != nil {
		if options.Auth.Token != "" {
			remoteOptions = append(remoteOptions, remote.WithAuth(&authn.Bearer{Token: options.Auth.Token}))
		} else if options.Auth.Username != "" || options.Auth.Password != "" {
			remoteOptions = append(remoteOptions, remote.WithAuth(&authn.Basic{
				Username: options.Auth.Username,
				Password: options.Auth.Password,
			}))
		}
	}
	return remoteOptions, nil
}

func findOCIChartLayer(layers []v1.Descriptor) (v1.Descriptor, error) {
	for _, layer := range layers {
		mediaType := string(layer.MediaType)
		if mediaType == ociChartLayerMediaType || mediaType == ociLegacyChartLayerMediaType {
			return layer, nil
		}
	}
	return v1.Descriptor{}, fmt.Errorf("OCI manifest has no Helm chart layer")
}

func isExactChartVersion(version string) bool {
	version = strings.TrimSpace(version)
	if exact, found := strings.CutPrefix(version, "="); found {
		version = strings.TrimSpace(exact)
	}
	_, err := semver.StrictNewVersion(strings.TrimPrefix(version, "v"))
	return err == nil
}

func equalDigest(actual, expected string) bool {
	return strings.EqualFold(strings.TrimPrefix(actual, "sha256:"), strings.TrimPrefix(expected, "sha256:"))
}
