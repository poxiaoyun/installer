package memoryfs

import (
	"io/fs"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestFS(t *testing.T) {
	data := []byte("chart")
	modTime := time.Unix(123, 0)
	fsys := New(map[string]File{
		"charts/demo.tgz": {Data: data, Mode: 0o400, ModTime: modTime},
		"values.yaml":     {Data: []byte("value: one"), Mode: 0o444},
	})
	data[0] = 'x'

	got, err := fsys.ReadFile("charts/demo.tgz")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "chart" {
		t.Fatalf("ReadFile() = %q, want chart", got)
	}
	info, err := fsys.Stat("charts/demo.tgz")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode() != 0o400 || !info.ModTime().Equal(modTime) {
		t.Fatalf("Stat() mode/modTime = %v %v", info.Mode(), info.ModTime())
	}

	var paths []string
	err = fs.WalkDir(fsys, ".", func(name string, _ fs.DirEntry, err error) error {
		if err == nil {
			paths = append(paths, name)
		}
		return err
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
	want := []string{".", "charts", "charts/demo.tgz", "values.yaml"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("WalkDir() paths = %v, want %v", paths, want)
	}

	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if data, err := fsys.ReadFile("charts/demo.tgz"); err != nil || string(data) != "chart" {
				t.Errorf("concurrent ReadFile() = %q, %v", data, err)
			}
		}()
	}
	wait.Wait()
}
