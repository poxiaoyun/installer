// Package osfs implements filesystem.WriteFS using the operating system.
package osfs

import (
	"io/fs"
	"os"
	"path/filepath"

	"xiaoshiai.cn/installer/install/filesystem"
)

type FS struct{}

func New() *FS {
	return &FS{}
}

func (*FS) Open(name string) (fs.File, error) {
	return os.Open(filepath.FromSlash(name))
}

func (*FS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(filepath.FromSlash(name))
}

func (*FS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(filepath.FromSlash(name))
}

func (*FS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.FromSlash(name))
}

func (*FS) OpenFile(name string, flag int, perm fs.FileMode) (filesystem.File, error) {
	return os.OpenFile(filepath.FromSlash(name), flag, perm)
}

func (*FS) CreateTemp(dir, pattern string) (filesystem.File, error) {
	return os.CreateTemp(filepath.FromSlash(dir), pattern)
}

func (*FS) MkdirTemp(dir, pattern string) (string, error) {
	return os.MkdirTemp(filepath.FromSlash(dir), pattern)
}

func (*FS) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(filepath.FromSlash(path), perm)
}

func (*FS) Mkdir(path string, perm fs.FileMode) error {
	return os.Mkdir(filepath.FromSlash(path), perm)
}

func (*FS) Rename(oldpath, newpath string) error {
	return os.Rename(filepath.FromSlash(oldpath), filepath.FromSlash(newpath))
}

func (*FS) Remove(path string) error {
	return os.Remove(filepath.FromSlash(path))
}

func (*FS) RemoveAll(path string) error {
	return os.RemoveAll(filepath.FromSlash(path))
}

var _ filesystem.WriteFS = (*FS)(nil)
