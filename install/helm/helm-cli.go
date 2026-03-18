package helm

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/repo"
	"xiaoshiai.cn/installer/utils"
	"xiaoshiai.cn/installer/version"
)

type ApplyOptions struct {
	DryRun  bool
	Repo    string
	Version string
}

// Download helm chart into cachedir saved as {name}-{version}.tgz file.
func Download(ctx context.Context, repo, name, version, cachedir, username, password string) (string, *chart.Chart, error) {
	// check exists
	filename := filepath.Join(cachedir, name+"-"+version+".tgz")
	if _, err := os.Stat(filename); err == nil {
		chart, err := loader.Load(filename)
		if err != nil {
			return filename, nil, err
		}
		return filename, chart, nil
	}
	chartPath, chart, err := LoadAndUpdateChart(ctx, repo, name, version, username, password)
	if err != nil {
		return "", nil, err
	}
	intofile, err := filepath.Abs(filepath.Join(cachedir, filepath.Base(chartPath)))
	if err != nil {
		return "", nil, err
	}
	if chartPath == intofile {
		return chartPath, chart, nil
	}
	os.MkdirAll(filepath.Dir(intofile), DefaultDirectoryMode)
	return intofile, chart, utils.RenameFile(chartPath, intofile)
}

// name is the name of the chart
// repo is the url of the chart repository,eg: http://charts.example.com
// if repopath is not empty,download it from repo and set chartNameOrPath to repo/repopath.
// LoadChart loads the chart from the repository
func LoadAndUpdateChart(ctx context.Context, repo, nameOrPath, version, username, password string) (string, *chart.Chart, error) {
	chartPath, err := LocateChartSuper(ctx, repo, nameOrPath, version, username, password)
	if err != nil {
		return "", nil, err
	}
	chart, err := loader.Load(chartPath)
	if err != nil {
		return "", nil, err
	}
	// dependencies update
	if err := action.CheckDependencies(chart, chart.Metadata.Dependencies); err != nil {
		settings := cli.New()
		man := &downloader.Manager{
			Out:              log.Default().Writer(),
			ChartPath:        chartPath,
			SkipUpdate:       false,
			Getters:          getter.All(settings),
			RepositoryConfig: settings.RepositoryConfig,
			RepositoryCache:  settings.RepositoryCache,
			Debug:            settings.Debug,
		}
		if err := man.Update(); err != nil {
			return "", nil, err
		}
		chart, err = loader.Load(chartPath)
		if err != nil {
			return "", nil, err
		}
	}
	return chartPath, chart, nil
}

func LocateChartSuper(ctx context.Context, repoURL, name, version, username, password string) (string, error) {
	repou, err := url.Parse(repoURL)
	if err != nil {
		return "", err
	}
	if repou.Scheme != FileProtocolSchema {
		return downloadChart(ctx, repoURL, name, version, username, password)
	}
	// handle file:// schema
	index, err := LoadIndex(ctx, repoURL)
	if err != nil {
		return "", err
	}
	cv, err := index.Get(name, version)
	if err != nil {
		return "", err
	}
	if len(cv.URLs) == 0 {
		return "", fmt.Errorf("%v has no downloadable URLs", cv)
	}

	downloadu, err := url.Parse(cv.URLs[0])
	if err != nil {
		return "", fmt.Errorf("parse chart download url: %w", err)
	}

	if !strings.HasSuffix(repou.Path, "/") {
		repou.Path += "/"
	}
	return repou.ResolveReference(downloadu).Path, nil
}

func downloadChart(_ context.Context, repourl, name, version, username, password string) (string, error) {
	settings := cli.New()
	dl := downloader.ChartDownloader{
		Out:              os.Stdout,
		Getters:          getter.All(settings),
		RepositoryConfig: settings.RepositoryConfig,
		RepositoryCache:  settings.RepositoryCache,
		Options: []getter.Option{
			getter.WithUserAgent(InstallerUserAgent()),
			getter.WithInsecureSkipVerifyTLS(true),
		},
	}
	if username != "" || password != "" {
		dl.Options = append(dl.Options,
			getter.WithBasicAuth(username, password),
		)
	}
	// nolint nestif
	if repourl != "" {
		if registry.IsOCI(repourl) {
			insecureHTTPClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			}
			registryOpts := []registry.ClientOption{
				registry.ClientOptDebug(settings.Debug),
				registry.ClientOptWriter(os.Stderr),
				registry.ClientOptCredentialsFile(settings.RegistryConfig),
				registry.ClientOptHTTPClient(insecureHTTPClient),
			}
			if username != "" {
				registryOpts = append(registryOpts, registry.ClientOptBasicAuth(username, password))
			}
			registryClient, err := registry.NewClient(registryOpts...)
			if err != nil {
				return "", err
			}
			dl.RegistryClient = registryClient
			dl.Options = append(dl.Options, getter.WithRegistryClient(registryClient))
			name = repourl
		} else {
			chartURL, err := repo.FindChartInAuthAndTLSAndPassRepoURL(
				repourl,
				username, password,
				name, version,
				"", "", "", // cert key ca
				true, username != "", // insecureTLS passCredentialsAll
				dl.Getters)
			if err != nil {
				return "", err
			}
			name = chartURL
		}
	}
	if err := os.MkdirAll(settings.RepositoryCache, DefaultDirectoryMode); err != nil {
		return "", err
	}
	filename, _, err := dl.DownloadTo(name, version, settings.RepositoryCache)
	if err != nil {
		return filename, fmt.Errorf("failed to download %s: %w", name, err)
	}
	return filename, nil
}

func InstallerUserAgent() string {
	return "installer/" + version.Get().GitVersion
}
