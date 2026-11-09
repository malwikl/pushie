package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Held by the production entrypoint for its entire lifetime. Never unlink a
// lock file: that would allow another process to lock a different inode.
func lockStore(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		parent, e := filepath.EvalSymlinks(filepath.Dir(path))
		if e != nil {
			return nil, e
		}
		canonical = filepath.Join(parent, filepath.Base(path))
	} else if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(canonical+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("database is already in use by another server: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
