//go:build windows

package sessionstore

import (
	"encoding/json"
	"errors"
	"golang.org/x/sys/windows"
	"nimbus/internal/controlclient"
	"os"
	"path/filepath"
	"unsafe"
)

func LockDirectory(dir string) error {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func Save(path string, s controlclient.Session) error {
	raw, err := json.Marshal(s)
	if err != nil || len(raw) == 0 {
		return errors.New("SESSION_ENCODE_FAILED")
	}
	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	if windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_LOCAL_MACHINE|windows.CRYPTPROTECT_UI_FORBIDDEN, &out) != nil {
		return errors.New("SESSION_PROTECT_FAILED")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	encrypted := unsafe.Slice(out.Data, int(out.Size))
	temp, err := os.CreateTemp(filepath.Dir(path), "session-*.tmp")
	if err != nil {
		return errors.New("SESSION_WRITE_FAILED")
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(encrypted)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil || closeErr != nil {
		return errors.New("SESSION_WRITE_FAILED")
	}
	from, _ := windows.UTF16PtrFromString(temp.Name())
	to, _ := windows.UTF16PtrFromString(path)
	if windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH) != nil {
		return errors.New("SESSION_COMMIT_FAILED")
	}
	return nil
}

func Load(path string) (controlclient.Session, error) {
	var s controlclient.Session
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > 1<<20 {
		return s, errors.New("SESSION_READ_FAILED")
	}
	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	if windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out) != nil {
		return s, errors.New("SESSION_UNPROTECT_FAILED")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	plain := unsafe.Slice(out.Data, int(out.Size))
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
	}()
	if json.Unmarshal(plain, &s) != nil {
		return s, errors.New("INVALID_SAVED_SESSION")
	}
	return s, nil
}
