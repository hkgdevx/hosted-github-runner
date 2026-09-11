//go:build linux

package ghrctl

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lock(dir string) (func(), error) {
	if e := secureDir(dir); e != nil {
		return nil, e
	}
	p := filepath.Join(dir, ".lock")
	if e := safePath(p); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another ghrctl command is modifying this configuration")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func socketGID(path string) (uint32, error) {
	st, e := os.Stat(path)
	if e != nil {
		return 0, e
	}
	if st.Mode()&os.ModeSocket == 0 {
		return 0, fmt.Errorf("%s is not a Unix socket", path)
	}
	return st.Sys().(*syscall.Stat_t).Gid, nil
}
