package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

type heldBackend struct {
	started, release, done chan struct{}
	lateReady              bool
	fail                   bool
}

func (b *heldBackend) Countries() []Country { return []Country{{Code: "SG", Name: "Singapore"}} }
func (b *heldBackend) Run(ctx context.Context, _ string, _ Settings, ready func(string)) error {
	close(b.started)
	if b.fail {
		return errors.New("REAL_FORWARDING_FAILED")
	}
	if !b.lateReady {
		ready("SG")
	}
	<-ctx.Done()
	if b.lateReady {
		ready("SG")
	}
	<-b.release
	close(b.done)
	return nil
}
func waitState(t *testing.T, c *Controller, state string) {
	t.Helper()
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) {
		if snapshot(c).State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state never became %s", state)
}
func TestRealDisconnectWaitsForCleanupAndRejectsStaleReady(t *testing.T) {
	b := &heldBackend{started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}), lateReady: true}
	c := NewReal(b)
	c.Handle(request("c", "connect", "{}"))
	<-b.started
	if snapshot(c).State == "connected" {
		t.Fatal("connected before verification")
	}
	c.Handle(request("d", "disconnect", "{}"))
	if snapshot(c).State != "disconnecting" {
		t.Fatal("disconnect skipped teardown")
	}
	if r := c.Handle(request("second", "connect", "{}")); r.Error == nil {
		t.Fatal("new connection during unresolved cleanup")
	}
	close(b.release)
	<-b.done
	waitState(t, c, "disconnected")
	if snapshot(c).Simulation {
		t.Fatal("real backend marked simulation")
	}
	c.Close()
}
func TestRealFailureNeverReportsConnected(t *testing.T) {
	b := &heldBackend{started: make(chan struct{}), fail: true}
	c := NewReal(b)
	defer c.Close()
	c.Handle(request("c", "connect", "{}"))
	<-b.started
	waitState(t, c, "disconnected")
	if snapshot(c).LastError != "REAL_FORWARDING_FAILED" {
		t.Fatal("real failure lost")
	}
}
