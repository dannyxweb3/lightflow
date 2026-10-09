//go:build !windows

package main

import (
	"context"
	"errors"
	"os/exec"
)

func verifyCoreTUN(context.Context, string, string, int) error {
	return errors.New("WINDOWS_ONLY_PROTOTYPE")
}

func bindGatewayInterface(context.Context, string, string, []byte) ([]byte, error) {
	return nil, errors.New("WINDOWS_ONLY_PROTOTYPE")
}

func hide(*exec.Cmd)                                   {}
func privatePipe() (string, []string, error)           { return "", nil, errors.New("WINDOWS_ONLY_PROTOTYPE") }
func verifyPipe(context.Context, string) error         { return errors.New("WINDOWS_ONLY_PROTOTYPE") }
func reloadPipe(context.Context, string, string) error { return errors.New("WINDOWS_ONLY_PROTOTYPE") }
func requireElevation() error                          { return errors.New("WINDOWS_ONLY_PROTOTYPE") }
func beginExperiment(context.Context, string, string) (func() error, error) {
	return nil, errors.New("WINDOWS_ONLY_PROTOTYPE")
}
func proveTUN(context.Context, string, string) error { return errors.New("WINDOWS_ONLY_PROTOTYPE") }
func ownCoreJob(*exec.Cmd) (func(), error)           { return nil, errors.New("WINDOWS_ONLY_PROTOTYPE") }
