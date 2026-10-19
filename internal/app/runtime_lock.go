package app

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// A local process lock protects borrowed daemon configuration as well as tsnet
// state. Status reads do not acquire an identity or this exclusive runtime lock.
func lockRuntime(dir string) (*os.File, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "runtime.lock")
	fd, e := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another process owns this component's state: %w", e)
	}
	return f, nil
}
