//go:build !windows

package camera

import "os/exec"

func hideWindow(cmd *exec.Cmd)                     {}
func protectProcess(cmd *exec.Cmd) (func(), error) { return func() {}, nil }
