package kustomize

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

type readOnlyFS struct {
	fs.FS
}

func (f readOnlyFS) Stat(name string) (fs.FileInfo, error) {
	return fs.Stat(f.FS, name)
}

func (f readOnlyFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(f.FS, name)
}

func (f readOnlyFS) ReadFile(name string) ([]byte, error) {
	return fs.ReadFile(f.FS, name)
}

func TestKustomizeFSReturnsPermissionErrorForWritesToReadOnlyFS(t *testing.T) {
	fsys := newKustomizeFS(readOnlyFS{FS: fstest.MapFS{}})

	if _, err := fsys.Create("file"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Create() error = %v", err)
	}
	if err := fsys.Mkdir("directory"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := fsys.MkdirAll("directory/child"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.RemoveAll("directory"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("RemoveAll() error = %v", err)
	}
}
