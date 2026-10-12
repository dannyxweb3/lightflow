//go:build windows

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"nimbus/internal/client/desktop"
	"nimbus/internal/client/sessionstore"
	"nimbus/internal/controlclient"
	"nimbus/internal/daemon"
	"nimbus/internal/ipc"
	"os"
	"path/filepath"
	"time"
)

func main() {
	root := flag.String("root", "", "admin-owned installed runtime")
	provision := flag.Bool("provision", false, "developer enrollment from existing local account; password is never saved")
	account := flag.String("account", "", "existing local account file")
	control := flag.String("control", "", "strict HTTPS origin")
	owner := flag.String("owner-sid", "", "authorized interactive Windows SID")
	hash := flag.String("worker-sha256", "", "installed worker digest")
	flag.Parse()
	var err error
	if *provision {
		err = enroll(*root, *account, *control, *owner, *hash)
	} else {
		service, _ := svc.IsWindowsService()
		if !service {
			err = errors.New("WINDOWS_SERVICE_REQUIRED")
		} else {
			err = svc.Run(desktop.ServiceName, &handler{root: *root})
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "LIGHTFLOW_SERVICE_OPERATION_FAILED")
		os.Exit(1)
	}
}

func enroll(root, account, control, owner, hash string) error {
	if _, err := windows.StringToSid(owner); err != nil {
		return err
	}
	if len(hash) != 64 {
		return errors.New("INVALID_WORKER_HASH")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := sessionstore.LockDirectory(root); err != nil {
		return err
	}
	sessionFile := filepath.Join(root, "session.dpapi")
	if _, err := os.Stat(sessionFile + ".lease"); err == nil {
		return errors.New("PREVIOUS_LEASE_UNRESOLVED")
	}
	client, err := controlclient.New(control, "")
	if err != nil {
		return err
	}
	defer client.Close()
	if account == "" {
		data, _ := json.Marshal(desktop.Config{Control: control, OwnerSID: owner, WorkerSHA256: hash})
		return os.WriteFile(filepath.Join(root, "service.json"), data, 0600)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var key ed25519.PrivateKey
	if saved, err := sessionstore.Load(sessionFile); err == nil && saved.Control == control {
		key = saved.DeviceKey
	}
	raw, err := os.ReadFile(account)
	if err != nil {
		return err
	}
	var credentials struct{ Email, Password string }
	if json.Unmarshal(raw, &credentials) != nil {
		return errors.New("INVALID_ACCOUNT")
	}
	if err := client.Login(ctx, credentials.Email, credentials.Password); err != nil {
		return err
	}
	credentials.Password = ""
	for i := range raw {
		raw[i] = 0
	}
	if len(key) != ed25519.PrivateKeySize {
		_, key, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
	}
	if err := client.RegisterDevice(ctx, key); err != nil {
		return err
	}
	if err := sessionstore.Save(sessionFile, client.Session()); err != nil {
		return err
	}
	countries, err := client.Countries(ctx)
	if err != nil {
		return err
	}
	config := desktop.Config{Control: control, OwnerSID: owner, WorkerSHA256: hash}
	for _, country := range countries {
		if country.Available {
			config.Countries = append(config.Countries, daemon.Country{Code: country.Code, Name: country.Name})
		}
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(root, "service.json"), data, 0600); err != nil {
		return err
	}
	fmt.Println("SERVICE_ENROLLMENT_OK")
	return nil
}

type handler struct{ root string }

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	raw, err := os.ReadFile(filepath.Join(h.root, "service.json"))
	var config desktop.Config
	if err != nil || json.Unmarshal(raw, &config) != nil {
		return true, 1
	}
	backend := &desktop.Backend{Root: h.root, Config: config}
	controller := daemon.NewReal(backend)
	if backend.Account().Authenticated {
		controller.Handle(daemon.Request{APIVersion: 1, RequestID: "startup-account-refresh", Method: "refresh_account", Params: json.RawMessage(`{}`)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ipc.ServeUser(ctx, desktop.PipeName, controller, config.OwnerSID, true) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			controller.Close()
			return err != nil, 1
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 60000}
				cancel()
				controller.Close()
				<-done
				return false, 0
			}
		}
	}
}
