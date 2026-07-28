//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stdout)
	return strings.TrimSpace(string(value)), err
}
