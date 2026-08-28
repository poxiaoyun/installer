package filesystem

import (
	"io"
	"io/fs"
)

// File is a writable fs.File.
type File interface {
	fs.File
	io.Writer
	Name() string
}

type FS interface {
	fs.FS
	Stat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	ReadFile(name string) ([]byte, error)
}

// Location identifies a file or directory within a filesystem.
type Location struct {
	FS   FS
	Path string
}

// WriteFS extends FS with the operations required by source downloads.
type WriteFS interface {
	FS
	OpenFile(name string, flag int, perm fs.FileMode) (File, error)
	CreateTemp(dir, pattern string) (File, error)
	MkdirTemp(dir, pattern string) (string, error)
	Mkdir(path string, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Rename(oldpath, newpath string) error
	Remove(path string) error
	RemoveAll(path string) error
}
