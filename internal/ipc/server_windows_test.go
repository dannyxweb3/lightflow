//go:build windows

package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"nimbus/internal/daemon"
)

func TestNamedPipeRoundTripAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	controller := daemon.New(true, time.Hour)
	name := fmt.Sprintf(`\\.\pipe\nimbus-test-%d`, time.Now().UnixNano())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, name, controller) }()
	t.Cleanup(func() {
		cancel()
		controller.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("pipe service failed to stop")
		}
	})
	dial := func() net.Conn {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			connection, err := winio.DialPipeContext(ctx, name)
			if err == nil {
				connection.SetDeadline(time.Now().Add(time.Second))
				return connection
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("pipe never became available")
		return nil
	}
	exchange := func(id, method, params string) daemon.Response {
		connection := dial()
		defer connection.Close()
		_, err := fmt.Fprintf(connection, `{"api_version":1,"request_id":%q,"method":%q,"params":%s}`+"\n", id, method, params)
		if err != nil {
			t.Fatal(err)
		}
		var response daemon.Response
		if err := json.NewDecoder(connection).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil || response.RequestID != id {
			t.Fatalf("invalid response: %+v", response)
		}
		return response
	}
	initial := exchange("read", "get_snapshot", "{}")
	if !initial.Result.Simulation || initial.Result.State != "disconnected" {
		t.Fatal("invalid initial state")
	}
	started := exchange("start", "connect", `{"country_code":"JP"}`)
	if started.Result.State != "authorizing" || started.Result.CountryCode != "JP" {
		t.Fatal("connection intent not applied")
	}
	stopped := exchange("stop", "disconnect", "{}")
	if stopped.Result.State != "disconnected" {
		t.Fatal("disconnect not applied")
	}
	// A malformed request must close the connection without changing state.
	connection := dial()
	fmt.Fprintln(connection, `{"api_version":1,"request_id":"bad","method":"connect","params":{},"unexpected":true}`)
	var response daemon.Response
	if json.NewDecoder(connection).Decode(&response) == nil {
		t.Fatal("unknown top-level field accepted")
	}
	connection.Close()
	if exchange("final", "get_snapshot", "{}").Result.State != "disconnected" {
		t.Fatal("malformed input changed state")
	}
}
