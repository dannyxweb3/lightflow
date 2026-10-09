//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"nimbus/internal/controlclient"
)

func verifyCoreTUN(ctx context.Context, pipe, device string, port int) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return winio.DialPipeContext(ctx, pipe) }}
	defer transport.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://core/configs", nil)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return errors.New("CORE_TUN_STATE_UNAVAILABLE")
	}
	defer response.Body.Close()
	var state struct {
		SOCKSPort int `json:"socks-port"`
		TUN       struct {
			Enable bool   `json:"enable"`
			Device string `json:"device"`
		} `json:"tun"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&state) != nil || !state.TUN.Enable || state.TUN.Device != device {
		return errors.New("CORE_TUN_CONFIG_NOT_APPLIED")
	}
	if state.SOCKSPort != port {
		return errors.New("CORE_SOCKS_PORT_NOT_APPLIED")
	}
	return nil
}

func hide(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

func pipeSDDL() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", errors.New("TOKEN_READ_FAILED")
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", errors.New("SID_READ_FAILED")
	}
	return "D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)", nil
}

func reloadPipe(ctx context.Context, pipe, path string) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return winio.DialPipeContext(ctx, pipe) }}
	defer transport.CloseIdleConnections()
	body, _ := json.Marshal(map[string]string{"path": path})
	request, _ := http.NewRequestWithContext(ctx, "PUT", "http://core/configs?force=true", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return errors.New("PIPE_CONFIG_RELOAD_FAILED")
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != 204 {
		return errors.New("PIPE_CONFIG_RELOAD_FAILED")
	}
	return nil
}

func privatePipe() (string, []string, error) {
	sddl, err := pipeSDDL()
	if err != nil {
		return "", nil, err
	}
	id, err := controlclient.RandomID()
	if err != nil {
		return "", nil, err
	}
	env := []string{}
	for _, item := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(item), "LISTEN_NAMEDPIPE_SDDL=") {
			env = append(env, item)
		}
	}
	env = append(env, "LISTEN_NAMEDPIPE_SDDL="+sddl)
	return `\\.\pipe\LightflowM0-` + id, env, nil
}

func verifyPipe(ctx context.Context, pipe string) error {
	sd, err := windows.GetNamedSecurityInfo(pipe, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errors.New("PIPE_ACL_READ_FAILED")
	}
	expected, err := pipeSDDL()
	if err != nil {
		return err
	}
	normalized, err := windows.SecurityDescriptorFromString(expected)
	if err != nil {
		return errors.New("PIPE_ACL_BUILD_FAILED")
	}
	if sd.String() != normalized.String() {
		return errors.New("PIPE_ACL_MISMATCH")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return winio.DialPipeContext(ctx, pipe) }}
	defer transport.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://core/version", nil)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return errors.New("PIPE_API_FAILED")
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != 200 {
		return errors.New("PIPE_API_FAILED")
	}
	return nil
}
