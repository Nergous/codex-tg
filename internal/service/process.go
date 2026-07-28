package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var errRefuseTestProcess = errors.New("refuse to start service from a test executable")

type processChild struct{ process *os.Process }

func (c processChild) Kill() error { return c.process.Kill() }

func startDetached() (Child, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if isTestExecutable(executable) {
		return nil, errRefuseTestProcess
	}
	command := exec.Command(executable, "serve")
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	configureDetached(command)
	if err := command.Start(); err != nil {
		return nil, err
	}
	go func() { _ = command.Wait() }()
	return processChild{process: command.Process}, nil
}

func isTestExecutable(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}
