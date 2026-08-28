package helm

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"helm.sh/helm/v4/pkg/action"
	chartapi "helm.sh/helm/v4/pkg/chart"
	chartarchive "helm.sh/helm/v4/pkg/chart/loader/archive"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/ignore"
	"xiaoshiai.cn/installer/install/filesystem"
)

func LoadChart(location filesystem.Location) (*chart.Chart, error) {
	loaded, err := loadChart(location)
	if err != nil {
		return nil, err
	}
	if err := action.CheckDependencies(loaded, chartDependencies(loaded)); err != nil {
		return nil, fmt.Errorf("chart dependencies are incomplete: %w", err)
	}
	return loaded, nil
}

func chartDependencies(loaded *chart.Chart) []chartapi.Dependency {
	dependencies := make([]chartapi.Dependency, len(loaded.Metadata.Dependencies))
	for index := range loaded.Metadata.Dependencies {
		dependencies[index] = loaded.Metadata.Dependencies[index]
	}
	return dependencies
}

func loadChart(location filesystem.Location) (*chart.Chart, error) {
	info, err := location.FS.Stat(location.Path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return loadChartDirectory(location.FS, location.Path)
	}
	file, err := location.FS.Open(location.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return loader.LoadArchive(file)
}

func loadChartDirectory(fsys filesystem.FS, directory string) (*chart.Chart, error) {
	rules := ignore.Empty()
	ignoreFile, err := fsys.Open(path.Join(directory, ignore.HelmIgnore))
	if err == nil {
		rules, err = ignore.Parse(ignoreFile)
		_ = ignoreFile.Close()
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	rules.AddDefaults()

	files := []*chartarchive.BufferedFile{}
	err = fs.WalkDir(fsys, directory, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := strings.TrimPrefix(strings.TrimPrefix(filename, directory), "/")
		if name == "" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if rules.Ignore(name, info) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fsys.ReadFile(filename)
		if err != nil {
			return err
		}
		files = append(files, &chartarchive.BufferedFile{
			Name:    name,
			ModTime: info.ModTime(),
			Data:    bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load chart directory: %w", err)
	}
	return loader.LoadFiles(files)
}
