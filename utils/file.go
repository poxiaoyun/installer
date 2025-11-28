package utils

import (
	"io"
	"os"
	"syscall"
)

func RenameFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	terr, ok := err.(*os.LinkError)
	if !ok {
		return err
	}
	if terr.Err != syscall.EXDEV {
		return err
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// nolint: nosnakecase
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
