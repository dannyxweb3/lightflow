//go:build !windows

package ipc

import (
	"context"
	"errors"
	"nimbus.local/client/internal/daemon"
)

func Serve(ctx context.Context, controller *daemon.Controller) error {
	return errors.New("this milestone implements Windows IPC only")
}
