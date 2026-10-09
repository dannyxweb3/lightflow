//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestOwnedJobKillsChildOnClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedJobChild$")
	child.Env = append(os.Environ(), "LIGHTFLOW_M0_JOB_CHILD=1")
	hide(child)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	closeJob, err := ownCoreJob(child)
	if err != nil {
		t.Fatal(err)
	}
	closeJob()
	child.Wait() // Kill-on-close may report exit status 0 on Windows.
	if ctx.Err() != nil {
		t.Fatal("owned child was not terminated by job close")
	}
}

func TestOwnedJobChild(t *testing.T) {
	if os.Getenv("LIGHTFLOW_M0_JOB_CHILD") == "1" {
		time.Sleep(30 * time.Second)
	}
}
