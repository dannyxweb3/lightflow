package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func request(id, method, params string) Request {
	return Request{APIVersion: 1, RequestID: id, Method: method, Params: json.RawMessage(params)}
}

func snapshot(c *Controller) Snapshot { return *c.Handle(request("read", "get_snapshot", "{}")).Result }

func TestDisconnectCancelsPriorAttempt(t *testing.T) {
	c := New(true, 10*time.Millisecond)
	defer c.Close()
	if r := c.Handle(request("connect", "connect", `{"country_code":"JP"}`)); r.Error != nil {
		t.Fatal(r.Error)
	}
	c.Handle(request("disconnect", "disconnect", "{}"))
	time.Sleep(80 * time.Millisecond)
	if s := snapshot(c); s.State != "disconnected" || s.CountryCode != "" {
		t.Fatalf("stale connection revived: %+v", s)
	}
}

func TestCancelledAttemptCannotOverwriteNewCountry(t *testing.T) {
	c := New(true, time.Millisecond)
	defer c.Close()
	c.Handle(request("first", "connect", `{"country_code":"JP"}`))
	c.Handle(request("stop", "disconnect", "{}"))
	c.Handle(request("second", "connect", `{"country_code":"US"}`))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s := snapshot(c); s.State == "connected" {
			if s.CountryCode != "US" || !s.Simulation {
				t.Fatalf("incorrect state: %+v", s)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("simulation did not complete")
}

func TestIdempotencyAndConflict(t *testing.T) {
	c := New(true, time.Hour)
	defer c.Close()
	first := c.Handle(request("same", "connect", `{"country_code":"JP"}`))
	duplicate := c.Handle(request("same", "connect", `{"country_code":"JP"}`))
	if first.Result.OperationID != duplicate.Result.OperationID {
		t.Fatal("duplicate started a new operation")
	}
	conflict := c.Handle(request("same", "connect", `{"country_code":"US"}`))
	if conflict.Error == nil || conflict.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("conflicting request accepted")
	}
}

func TestInvalidParamsNeverChangeNetworkIntent(t *testing.T) {
	c := New(true, time.Hour)
	defer c.Close()
	for i, params := range []string{`{"country_code":"XX"}`, `{"country_code":"JP","config":"evil"}`, `{} {}`, `{"country_code":123}`, `null`, `{"country_code":null}`} {
		if r := c.Handle(request(fmt.Sprint(i), "connect", params)); r.Error == nil {
			t.Fatalf("accepted %s", params)
		}
	}
	if snapshot(c).State != "disconnected" {
		t.Fatal("invalid request mutated state")
	}
}

func TestProductionFailsClosed(t *testing.T) {
	c := New(false, time.Millisecond)
	defer c.Close()
	if r := c.Handle(request("prod", "connect", "{}")); r.Error == nil || r.Error.Code != "NETWORK_ADAPTER_NOT_READY" {
		t.Fatal("production accepted simulated connection")
	}
}

func TestSettingsRequireDisconnectedAndDirectCannotConnect(t *testing.T) {
	c := New(true, time.Hour)
	defer c.Close()
	c.Handle(request("c", "connect", "{}"))
	r := c.Handle(request("s", "update_settings", `{"mode":"direct","transport_preference":"auto","allow_lan":true}`))
	if r.Error == nil || r.Error.Code != "DISCONNECT_BEFORE_SETTINGS" {
		t.Fatal("active settings changed")
	}
	c.Handle(request("d", "disconnect", "{}"))
	c.Handle(request("s2", "update_settings", `{"mode":"direct","transport_preference":"auto","allow_lan":true}`))
	if r := c.Handle(request("c2", "connect", "{}")); r.Error == nil || r.Error.Code != "DIRECT_MODE" {
		t.Fatal("direct connected")
	}
}
