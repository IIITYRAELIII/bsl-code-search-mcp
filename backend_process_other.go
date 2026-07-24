//go:build !windows

package main

import "os/exec"

func bindBackendProcess(_ *exec.Cmd) (func(), error) {
	return func() {}, nil
}
