//go:build windows

package main

import "errors"

func lockDataDir(string) (func(), error) {
	return nil, errors.New("This release supports macOS and Linux; use WSL on Windows")
}
