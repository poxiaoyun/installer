package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/sync/singleflight"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem"
	"xiaoshiai.cn/installer/install/source"
	"xiaoshiai.cn/installer/version"
)

const (
	defaultDirMode  = 0o755
	defaultFileMode = 0o644
)

type Downloader struct {
	CacheDir string
	FS       filesystem.WriteFS
	flight   singleflight.Group
}

type DownloadOptions struct {
	Type    SourceType
	URL     string
	Name    string
	Version string
	Subpath string
	Auth    *install.ResolvedAuth
	TLS     *install.ResolvedTLS
}

type SourceType string

const (
	SourceTypeFile  SourceType = "file"
	SourceTypeGit   SourceType = "git"
	SourceTypeZip   SourceType = "zip"
	SourceTypeTarGz SourceType = "tar.gz"
	SourceTypeChart SourceType = "chart"
)

func NewDownloader(cacheDir string, fsys filesystem.WriteFS) *Downloader {
	return &Downloader{CacheDir: cacheDir, FS: fsys}
}

// we cache "bundle" in a directory with name
// "{repo host}/{name}-{version} or {repo host}/{name}-{version}.tgz" under cache directory
func (d *Downloader) Download(ctx context.Context, options DownloadOptions) (filesystem.Location, error) {
	if options.Type == "" {
		options.Type = detectSourceType(options.URL)
	}
	result, err, _ := d.flight.Do(downloadFlightKey(options), func() (any, error) {
		return d.download(ctx, options)
	})
	if err != nil {
		return filesystem.Location{}, err
	}
	return result.(filesystem.Location), nil
}

func detectSourceType(sourceURL string) SourceType {
	switch {
	case strings.HasPrefix(sourceURL, "file://"):
		return SourceTypeFile
	case strings.HasPrefix(sourceURL, "oci://"):
		return SourceTypeChart
	case strings.HasSuffix(sourceURL, ".git"):
		return SourceTypeGit
	case strings.HasSuffix(sourceURL, ".zip"):
		return SourceTypeZip
	case strings.HasSuffix(sourceURL, ".tar.gz"), strings.HasSuffix(sourceURL, ".tgz"):
		return SourceTypeTarGz
	default:
		return SourceTypeChart
	}
}

func (d *Downloader) download(ctx context.Context, options DownloadOptions) (filesystem.Location, error) {
	cacheBase := DownloadCacheBase(d.CacheDir, options.URL)
	switch options.Type {
	case SourceTypeFile:
		return downloadFileSource(d.FS, options)
	case SourceTypeGit:
		return downloadGitSource(ctx, cacheBase, d.FS, options)
	case SourceTypeZip:
		return downloadZipSource(ctx, cacheBase, d.FS, options)
	case SourceTypeTarGz:
		return downloadTarGzSource(ctx, cacheBase, d.FS, options)
	case SourceTypeChart:
		return downloadChartSource(ctx, cacheBase, d.FS, options)
	default:
		return filesystem.Location{}, fmt.Errorf("unsupported source type %q", options.Type)
	}
}

func DownloadCacheBase(cacheDir, repositoryURL string) string {
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = path.Join(home, ".cache", "installer")
	}
	repository, err := url.Parse(repositoryURL)
	if err != nil {
		return cacheDir
	}
	host := repository.Hostname()
	if port := repository.Port(); port != "" {
		host += "-" + port
	}
	return path.Join(cacheDir, host, repository.Path)
}

func downloadFileSource(fsys filesystem.WriteFS, options DownloadOptions) (filesystem.Location, error) {
	reference, err := url.ParseRequestURI(options.URL)
	if err != nil {
		return filesystem.Location{}, err
	}
	if reference.Host != "" && reference.Host != "localhost" {
		return filesystem.Location{}, fmt.Errorf("unsupported host: %s", reference.Host)
	}
	return filesystem.Location{FS: fsys, Path: reference.Path}, nil
}

func downloadGitSource(ctx context.Context, cacheBase string, fsys filesystem.WriteFS, options DownloadOptions) (filesystem.Location, error) {
	destination := path.Join(cacheBase, bundleCacheFilename(options))
	location := filesystem.Location{FS: fsys, Path: destination}
	if options.Version != "" && pathExists(fsys, destination) {
		return location, nil
	}
	err := atomicDownloadDirectory(fsys, destination, func(temporary string) error {
		return DownloadGit(ctx, temporary, fsys, options)
	})
	return location, err
}

func downloadZipSource(ctx context.Context, cacheBase string, fsys filesystem.WriteFS, options DownloadOptions) (filesystem.Location, error) {
	destination := path.Join(cacheBase, bundleCacheFilename(options))
	location := filesystem.Location{FS: fsys, Path: destination}
	if options.Version != "" && pathExists(fsys, destination) {
		return location, nil
	}
	err := atomicDownloadDirectory(fsys, destination, func(temporary string) error {
		return DownloadZip(ctx, temporary, fsys, options)
	})
	return location, err
}

func downloadTarGzSource(ctx context.Context, cacheBase string, fsys filesystem.WriteFS, options DownloadOptions) (filesystem.Location, error) {
	destination := path.Join(cacheBase, bundleCacheFilename(options))
	location := filesystem.Location{FS: fsys, Path: destination}
	if options.Version != "" && pathExists(fsys, destination) {
		return location, nil
	}
	err := atomicDownloadDirectory(fsys, destination, func(temporary string) error {
		return DownloadTgz(ctx, temporary, fsys, options)
	})
	return location, err
}

func downloadChartSource(ctx context.Context, cacheBase string, fsys filesystem.WriteFS, options DownloadOptions) (filesystem.Location, error) {
	source := ChartOptions{
		Repository: options.URL,
		Name:       options.Name,
		Version:    options.Version,
		Auth:       options.Auth,
		TLS:        options.TLS,
	}
	if _, digestReference := ociChartDigest(options.URL); isExactRepositoryChart(options) || digestReference {
		destination := path.Join(cacheBase, chartCacheFilename(options))
		location := filesystem.Location{FS: fsys, Path: destination}
		if pathExists(fsys, destination) {
			return location, nil
		}
		resolved, err := resolveChart(ctx, source)
		if err != nil {
			return filesystem.Location{}, err
		}
		_, err = downloadResolvedChart(ctx, destination, fsys, resolved)
		return location, err
	}
	resolved, err := resolveChart(ctx, source)
	if err != nil {
		return filesystem.Location{}, err
	}
	options.Version = cmp.Or(resolved.Digest, resolved.Version)
	destination := path.Join(cacheBase, chartCacheFilename(options))
	location := filesystem.Location{FS: fsys, Path: destination}
	if pathExists(fsys, destination) {
		return location, nil
	}
	_, err = downloadResolvedChart(ctx, destination, fsys, resolved)
	return location, err
}

func bundleCacheFilename(options DownloadOptions) string {
	filename := options.Name
	if options.Version != "" {
		filename += "-" + options.Version
	}
	if options.Subpath != "" {
		filename += "-" + options.Subpath
	}
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(filename)
}

func chartCacheFilename(options DownloadOptions) string {
	if digest, found := ociChartDigest(options.URL); found {
		options.Version = digest
	}
	return bundleCacheFilename(options) + ".tgz"
}

func isExactRepositoryChart(options DownloadOptions) bool {
	return !strings.HasPrefix(options.URL, "oci://") && isExactChartVersion(options.Version)
}

func ociChartDigest(repository string) (string, bool) {
	_, digest, found := strings.Cut(repository, "@")
	return digest, found && strings.HasPrefix(digest, "sha256:")
}

func pathExists(fsys filesystem.FS, filename string) bool {
	_, err := fsys.Stat(filename)
	return err == nil
}

func atomicDownloadDirectory(
	fsys filesystem.WriteFS,
	destination string,
	download func(string) error,
) error {
	cacheBase := path.Dir(destination)
	if err := fsys.MkdirAll(cacheBase, defaultDirMode); err != nil {
		return err
	}
	temporary, err := fsys.MkdirTemp(cacheBase, "download-*")
	if err != nil {
		return err
	}
	defer fsys.RemoveAll(temporary)
	if err := download(temporary); err != nil {
		return err
	}
	if err := fsys.RemoveAll(destination); err != nil {
		return err
	}
	return fsys.Rename(temporary, destination)
}

func atomicDownloadFile(
	fsys filesystem.WriteFS,
	destination string,
	download func(filesystem.File) error,
) error {
	cacheBase := path.Dir(destination)
	if err := fsys.MkdirAll(cacheBase, defaultDirMode); err != nil {
		return err
	}
	file, err := fsys.CreateTemp(cacheBase, "download-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer fsys.Remove(temporary)
	if err := download(file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return fsys.Rename(temporary, destination)
}

func downloadFlightKey(options DownloadOptions) string {
	return strings.Join([]string{options.URL, string(options.Type), options.Name, options.Version, options.Subpath}, "\x00")
}

func DownloadZip(ctx context.Context, destination string, fsys filesystem.WriteFS, options DownloadOptions) error {
	fetcher, err := newHTTPFetcher(options)
	if err != nil {
		return err
	}
	resp, err := fetcher.Open(ctx, options.URL)
	if err != nil {
		return err
	}
	defer resp.Close()
	raw, err := io.ReadAll(resp)
	if err != nil {
		return err
	}

	r := bytes.NewReader(raw)
	zipr, err := zip.NewReader(r, r.Size())
	if err != nil {
		return err
	}

	subpath := options.Subpath
	if subpath != "" && !strings.HasSuffix(subpath, "/") {
		subpath += "/"
	}

	for _, file := range zipr.File {
		if !strings.HasPrefix(file.Name, subpath) {
			continue
		}
		filename := path.Join(destination, strings.TrimPrefix(file.Name, subpath))

		if file.FileInfo().IsDir() {
			if err := fsys.MkdirAll(filename, file.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := fsys.MkdirAll(path.Dir(filename), defaultDirMode); err != nil {
			return err
		}
		dest, err := fsys.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(dest, src)
		_ = src.Close()
		closeErr := dest.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func DownloadTgz(ctx context.Context, destination string, fsys filesystem.WriteFS, options DownloadOptions) error {
	fetcher, err := newHTTPFetcher(options)
	if err != nil {
		return err
	}
	resp, err := fetcher.Open(ctx, options.URL)
	if err != nil {
		return err
	}
	defer resp.Close()

	return UnTarGz(fsys, resp, options.Subpath, destination)
}

func DownloadGit(ctx context.Context, destination string, fsys filesystem.WriteFS, options DownloadOptions) error {
	cloneOptions := gitCloneOptions(options)
	repository, err := git.CloneContext(ctx, memory.NewStorage(), nil, cloneOptions)
	if err != nil {
		return err
	}

	rev := options.Version
	if rev == "" {
		rev = "HEAD"
	}
	hash, err := repository.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return err
	}

	commit, err := repository.CommitObject(*hash)
	if err != nil {
		return err
	}

	tree, err := repository.TreeObject(commit.TreeHash)
	if err != nil {
		return err
	}

	return tree.Files().ForEach(func(f *object.File) error {
		if !strings.HasPrefix(f.Name, options.Subpath) {
			return nil
		}
		raw, err := f.Contents()
		if err != nil {
			return err
		}

		fmode, err := f.Mode.ToOSFileMode()
		if err != nil {
			fmode = defaultFileMode
		}

		filename := path.Join(destination, strings.TrimPrefix(f.Name, options.Subpath))
		if dir := path.Dir(filename); dir != "." {
			if err := fsys.MkdirAll(dir, defaultDirMode); err != nil {
				return err
			}
		}
		file, err := fsys.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fmode)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(file, raw)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	})
}

func gitCloneOptions(options DownloadOptions) *git.CloneOptions {
	cloneOptions := &git.CloneOptions{
		URL:          options.URL,
		Depth:        1,
		SingleBranch: true,
	}
	if options.TLS != nil {
		cloneOptions.InsecureSkipTLS = options.TLS.InsecureSkipVerify
		cloneOptions.CABundle = options.TLS.CAData
		cloneOptions.ClientCert = options.TLS.CertData
		cloneOptions.ClientKey = options.TLS.KeyData
	}
	if options.Auth != nil {
		if options.Auth.Token != "" {
			cloneOptions.Auth = &githttp.BasicAuth{Username: "oauth2", Password: options.Auth.Token}
		} else {
			username := options.Auth.Username
			if username == "" {
				username = "git"
			}
			cloneOptions.Auth = &githttp.BasicAuth{Username: username, Password: options.Auth.Password}
		}
	}
	return cloneOptions
}

func newHTTPFetcher(options DownloadOptions) (*source.HTTPFetcher, error) {
	httpOptions := source.HTTPOptions{
		BaseURL:   options.URL,
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

func installerUserAgent() string {
	return "installer/" + version.Get().GitVersion
}

func UnTarGz(fsys filesystem.WriteFS, r io.Reader, subpath, into string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if !strings.HasPrefix(hdr.Name, subpath) {
			continue
		}

		filename := strings.TrimPrefix(hdr.Name, subpath)
		filename = path.Join(into, filename)

		if hdr.FileInfo().IsDir() {
			if err := fsys.MkdirAll(filename, defaultDirMode); err != nil {
				return err
			}
			continue
		}
		if err := fsys.MkdirAll(path.Dir(filename), defaultDirMode); err != nil {
			return err
		}

		dest, err := fsys.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(dest, tr)
		closeErr := dest.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
