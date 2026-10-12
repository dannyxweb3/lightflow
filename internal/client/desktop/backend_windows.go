//go:build windows

package desktop

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"golang.org/x/sys/windows"
	"nimbus/internal/daemon"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const ServiceName = "LightflowDesktopDev"
const PipeName = `\\.\pipe\lightflow-desktop-real-v1`

type Config struct {
	Control, OwnerSID, WorkerSHA256 string
	Countries                       []daemon.Country
}
type Backend struct {
	Root      string
	Config    Config
	accountMu sync.Mutex
}

func (b *Backend) Countries() []daemon.Country {
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	return append([]daemon.Country{}, b.Config.Countries...)
}
func (b *Backend) Run(ctx context.Context, country string, settings daemon.Settings, ready func(string)) error {
	session := filepath.Join(b.Root, "session.dpapi")
	if _, err := os.Stat(session + ".lease"); err == nil {
		return errors.New("PREVIOUS_LEASE_UNRESOLVED")
	}
	worker := filepath.Join(b.Root, "m0.exe")
	raw, err := os.ReadFile(worker)
	if err != nil {
		return errors.New("WORKER_UNAVAILABLE")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != strings.ToLower(b.Config.WorkerSHA256) {
		return errors.New("WORKER_HASH_MISMATCH")
	}
	cancelFile, err := os.CreateTemp(b.Root, "cancel-*")
	if err != nil {
		return errors.New("WORKER_START_FAILED")
	}
	stopPath := cancelFile.Name()
	cancelFile.Close()
	os.Remove(stopPath)
	defer os.Remove(stopPath)
	cmd := exec.Command(worker, "-production", "-tun", "-probe-domain", "-resolve-probe", "-desktop-session", "-session-file", session, "-stop-file", stopPath, "-country", country, "-account", "service-session", "-control", b.Config.Control, "-mode", settings.Mode)
	cmd.Dir = b.Root
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New("WORKER_START_FAILED")
	}
	// The worker emits only allowlisted diagnostics. Nothing is persisted here.
	failure := &safeFailure{}
	cmd.Stderr = failure
	if cmd.Start() != nil {
		return errors.New("WORKER_START_FAILED")
	}
	closeJob, err := ownJob(cmd)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	defer closeJob()
	events := make(chan string, 32)
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		defer close(events)
		s := bufio.NewScanner(output)
		for s.Scan() {
			line := s.Text()
			if strings.HasPrefix(line, "DESKTOP_") {
				events <- line
			}
		}
	}()
	ended := make(chan error, 1)
	go func() { <-scanned; ended <- cmd.Wait() }()
	actual := ""
	clean := false
	cancelled := false
	var grace <-chan time.Time
	cancelSignal := ctx.Done()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if strings.HasPrefix(event, "DESKTOP_COUNTRY=") {
				value := strings.TrimPrefix(event, "DESKTOP_COUNTRY=")
				if len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z' {
					actual = value
				}
			}
			if event == "DESKTOP_CONNECTED" && !cancelled {
				ready(actual)
			}
			if event == "DESKTOP_STOPPED_CLEAN" {
				clean = true
			}
		case <-cancelSignal:
			cancelled = true
			cancelSignal = nil
			if os.WriteFile(stopPath, []byte("stop"), 0600) != nil {
				cmd.Process.Kill()
			}
			grace = time.After(45 * time.Second)
		case <-grace:
			cmd.Process.Kill()
			<-ended
			return errors.New("NETWORK_RECOVERY_FAILED")
		case processErr := <-ended:
			<-scanned
			// Drain final stdout records already buffered when the process exited.
			if events != nil {
				for event := range events {
					if event == "DESKTOP_STOPPED_CLEAN" {
						clean = true
					}
				}
			}
			if _, err := os.Stat(session + ".lease"); err == nil {
				return errors.New("PREVIOUS_LEASE_UNRESOLVED")
			}
			if clean && processErr == nil {
				return nil
			}
			return errors.New(failure.Code())
		}
	}
}

type safeFailure struct {
	mu   sync.Mutex
	code string
}

func (s *safeFailure) Write(raw []byte) (int, error) {
	fields := strings.Fields(string(raw))
	if len(fields) > 1 && fields[0] == "FAIL:" && regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`).MatchString(fields[1]) {
		s.mu.Lock()
		s.code = fields[1]
		s.mu.Unlock()
	}
	return len(raw), nil
}
func (s *safeFailure) Code() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code != "" {
		return s.code
	}
	return "REAL_CONNECTION_FAILED"
}

func ownJob(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, errors.New("WORKER_JOB_FAILED")
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(job)
		return nil, errors.New("WORKER_JOB_FAILED")
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, errors.New("WORKER_JOB_FAILED")
	}
	defer windows.CloseHandle(process)
	if windows.AssignProcessToJobObject(job, process) != nil {
		windows.CloseHandle(job)
		return nil, errors.New("WORKER_JOB_FAILED")
	}
	return func() { windows.CloseHandle(job) }, nil
}
