//go:build windows

package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"nimbus/internal/daemon"
)

const PipeName = `\\.\pipe\nimbus-vpn-dev-v1`

// Development daemon runs unelevated as the interactive user. The DACL permits
// that user only. It is deliberately not the production privileged service pipe.
func Serve(ctx context.Context, controller *daemon.Controller) error {
	return serve(ctx, PipeName, controller)
}

func serve(ctx context.Context, name string, controller *daemon.Controller) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	dacl := "D:P(A;;GA;;;" + user.User.Sid.String() + ")"
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: dacl, InputBufferSize: 8192, OutputBufferSize: 65536})
	if err != nil {
		return fmt.Errorf("open development pipe: %w", err)
	}
	defer listener.Close()
	go func() { <-ctx.Done(); listener.Close() }()
	var handlers sync.WaitGroup
	defer handlers.Wait()
	semaphore := make(chan struct{}, 16)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case semaphore <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() { <-semaphore }()
			handle(conn, controller)
		}()
	}
}

func handle(conn net.Conn, controller *daemon.Controller) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(io.LimitReader(conn, 8193))
	payload, err := reader.ReadBytes('\n')
	if err != nil || len(payload) > 8192 {
		return
	}
	var req daemon.Request
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		return
	}
	json.NewEncoder(conn).Encode(controller.Handle(req))
}
