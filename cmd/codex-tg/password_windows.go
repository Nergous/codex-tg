//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	err := windows.GetConsoleMode(h, &mode)
	if err != nil {
		return "", err
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return "", err
	}
	defer windows.SetConsoleMode(h, mode)
	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(os.Stdout)
	return strings.TrimSpace(value), err
}
