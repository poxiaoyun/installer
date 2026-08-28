package kustomize

import (
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"sigs.k8s.io/kustomize/kyaml/filesys"
	"xiaoshiai.cn/installer/install/filesystem"
)

type kustomizeFS struct {
	fsys filesystem.FS
}

func newKustomizeFS(fsys filesystem.FS) filesys.FileSystem {
	return &kustomizeFS{fsys: fsys}
}

func (f *kustomizeFS) Create(name string) (filesys.File, error) {
	fsys, err := f.writeFS()
	if err != nil {
		return nil, err
	}
	return fsys.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
}

func (f *kustomizeFS) Mkdir(name string) error {
	fsys, err := f.writeFS()
	if err != nil {
		return err
	}
	return fsys.Mkdir(name, 0o755)
}

func (f *kustomizeFS) MkdirAll(name string) error {
	fsys, err := f.writeFS()
	if err != nil {
		return err
	}
	return fsys.MkdirAll(name, 0o755)
}

func (f *kustomizeFS) RemoveAll(name string) error {
	fsys, err := f.writeFS()
	if err != nil {
		return err
	}
	return fsys.RemoveAll(name)
}

func (f *kustomizeFS) Open(name string) (filesys.File, error) {
	file, err := f.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	if writable, ok := file.(filesys.File); ok {
		return writable, nil
	}
	return &readOnlyFile{File: file}, nil
}

func (f *kustomizeFS) IsDir(name string) bool {
	info, err := f.fsys.Stat(name)
	return err == nil && info.IsDir()
}

func (f *kustomizeFS) ReadDir(name string) ([]string, error) {
	entries, err := f.fsys.ReadDir(name)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index := range entries {
		names[index] = entries[index].Name()
	}
	return names, nil
}

func (f *kustomizeFS) CleanedAbs(name string) (filesys.ConfirmedDir, string, error) {
	cleaned := path.Clean(name)
	info, err := f.fsys.Stat(cleaned)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return filesys.ConfirmedDir(cleaned), "", nil
	}
	return filesys.ConfirmedDir(path.Dir(cleaned)), path.Base(cleaned), nil
}

func (f *kustomizeFS) Exists(name string) bool {
	_, err := f.fsys.Stat(name)
	return err == nil
}

func (f *kustomizeFS) Glob(pattern string) ([]string, error) {
	return fs.Glob(f.fsys, pattern)
}

func (f *kustomizeFS) ReadFile(name string) ([]byte, error) {
	return f.fsys.ReadFile(name)
}

func (f *kustomizeFS) WriteFile(name string, data []byte) error {
	file, err := f.Create(name)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (f *kustomizeFS) Walk(name string, walkFn filepath.WalkFunc) error {
	return fs.WalkDir(f.fsys, name, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkFn(filename, nil, walkErr)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return walkFn(filename, info, nil)
	})
}

func (f *kustomizeFS) writeFS() (filesystem.WriteFS, error) {
	fsys, ok := f.fsys.(filesystem.WriteFS)
	if !ok {
		return nil, fs.ErrPermission
	}
	return fsys, nil
}

type readOnlyFile struct {
	fs.File
}

func (*readOnlyFile) Write([]byte) (int, error) {
	return 0, fs.ErrPermission
}

var _ io.ReadWriteCloser = (*readOnlyFile)(nil)
