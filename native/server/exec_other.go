//go:build !windows

package main

import "os/exec"

func attachProcess(command *exec.Cmd) {}
