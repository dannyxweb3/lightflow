package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"nimbus/internal/daemon"
	"nimbus/internal/ipc"
)

func main() {
	development := flag.Bool("development", false, "explicit simulation; does not protect or modify network traffic")
	flag.Parse()
	if !*development {
		log.Fatal("NETWORK_ADAPTER_NOT_READY: use --development for the UI milestone; real VPN is not implemented")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	controller := daemon.New(true, 450*time.Millisecond)
	defer controller.Close()
	log.Print("Nimbus development daemon: simulated connections only; traffic is NOT protected")
	if err := ipc.Serve(ctx, controller); err != nil {
		log.Fatal(err)
	}
}
