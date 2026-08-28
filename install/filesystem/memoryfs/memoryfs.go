// Package memoryfs implements a read-only in-memory file tree.
package memoryfs

import (
	"bytes"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"xiaoshiai.cn/installer/install/filesystem"
)

type File struct {
	Data    []byte
	Mode    fs.FileMode
	ModTime time.Time
	Sys     any
}

type FS struct {
	files map[string]File
}

func New(files map[string]File) *FS {
	fsys := &FS{files: make(map[string]File, len(files))}
	for name, file := range files {
		file.Data = bytes.Clone(file.Data)
		fsys.files[path.Clean(name)] = file
	}
	return fsys
}

func (f *FS) Open(name string) (fs.File, error) {
	info, err := f.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		entries, err := f.ReadDir(name)
		if err != nil {
			return nil, err
		}
		return &directory{info: info, entries: entries}, nil
	}
	return &openFile{Reader: *bytes.NewReader(f.files[path.Clean(name)].Data), info: info}, nil
}

func (f *FS) Stat(name string) (fs.FileInfo, error) {
	name = path.Clean(name)
	if file, ok := f.files[name]; ok {
		return fileInfo{name: path.Base(name), file: file}, nil
	}
	if name == "." {
		return fileInfo{name: name, directory: true}, nil
	}
	prefix := name + "/"
	for filename := range f.files {
		if strings.HasPrefix(filename, prefix) {
			return fileInfo{name: path.Base(name), directory: true}, nil
		}
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = path.Clean(name)
	info, err := f.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	prefix := ""
	if name != "." {
		prefix = name + "/"
	}
	children := map[string]fileInfo{}
	for filename, file := range f.files {
		if !strings.HasPrefix(filename, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(filename, prefix)
		child, _, nested := strings.Cut(remainder, "/")
		if nested {
			children[child] = fileInfo{name: child, directory: true}
		} else {
			children[child] = fileInfo{name: child, file: file}
		}
	}
	names := make([]string, 0, len(children))
	for child := range children {
		names = append(names, child)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, len(names))
	for index, child := range names {
		entries[index] = children[child]
	}
	return entries, nil
}

func (f *FS) ReadFile(name string) ([]byte, error) {
	name = path.Clean(name)
	file, ok := f.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: fs.ErrNotExist}
	}
	return bytes.Clone(file.Data), nil
}

type openFile struct {
	bytes.Reader
	info fs.FileInfo
}

func (f *openFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (*openFile) Close() error                 { return nil }

type directory struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	offset  int
}

func (*directory) Read([]byte) (int, error)     { return 0, fs.ErrInvalid }
func (d *directory) Stat() (fs.FileInfo, error) { return d.info, nil }
func (*directory) Close() error                 { return nil }
func (d *directory) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.offset == len(d.entries) && n > 0 {
		return nil, io.EOF
	}
	end := len(d.entries)
	if n > 0 && d.offset+n < end {
		end = d.offset + n
	}
	entries := d.entries[d.offset:end]
	d.offset = end
	return entries, nil
}

type fileInfo struct {
	name      string
	file      File
	directory bool
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64 {
	if i.directory {
		return 0
	}
	return int64(len(i.file.Data))
}
func (i fileInfo) ModTime() time.Time {
	if i.directory {
		return time.Time{}
	}
	return i.file.ModTime
}
func (i fileInfo) IsDir() bool { return i.directory }
func (i fileInfo) Sys() any {
	if i.directory {
		return nil
	}
	return i.file.Sys
}
func (i fileInfo) Type() fs.FileMode { return i.Mode().Type() }
func (i fileInfo) Info() (fs.FileInfo, error) {
	return i, nil
}

func (i fileInfo) Mode() fs.FileMode {
	if i.directory {
		return fs.ModeDir | 0o555
	}
	return i.file.Mode
}

var _ filesystem.FS = (*FS)(nil)
