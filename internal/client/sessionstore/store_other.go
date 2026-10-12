//go:build !windows

package sessionstore

import (
	"errors"
	"nimbus/internal/controlclient"
)

func LockDirectory(string) error               { return errors.New("WINDOWS_ONLY") }
func Save(string, controlclient.Session) error { return errors.New("WINDOWS_ONLY") }
func Load(string) (controlclient.Session, error) {
	return controlclient.Session{}, errors.New("WINDOWS_ONLY")
}
