//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func lockDataDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".bridge.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("Could not lock data directory")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("Another Google Bridge is using this data directory")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
