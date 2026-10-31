//go:build linux && cgo

package linux

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestInstanceBusHelper(t *testing.T) {
	if os.Getenv("WORKSPACE_INSTANCE_BUS_CHILD") != "1" {
		t.Skip("private bus fixture only")
	}
	checker := DesktopInstances{}
	ctx := context.Background()
	const name = "org.example.WorkspaceInstanceFixture"
	if os.Getenv("WORKSPACE_INSTANCE_BUS_MISSING") == "1" {
		if got := checker.Check(ctx, name); got.State != "unknown" {
			t.Fatal(got)
		}
		return
	}
	if got := checker.Check(ctx, name); got.State != "not_running" {
		t.Fatal(got)
	}
	owner, err := dbus.Connect(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	reply, err := owner.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatal(reply, err)
	}
	if got := checker.Check(ctx, name); got.State != "running" {
		t.Fatal(got)
	}
	if _, err := owner.ReleaseName(name); err != nil {
		t.Fatal(err)
	}
	if got := checker.Check(ctx, name); got.State != "not_running" {
		t.Fatal(got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if got := checker.Check(cancelled, name); got.State != "unknown" {
		t.Fatal(got)
	}
	if got := checker.Check(ctx, ":1.42"); got.State != "unknown" {
		t.Fatal("unique connection accepted", got)
	}
}

func TestInstanceIdentityOnPrivateSessionBus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root := t.TempDir()
	config := filepath.Join(root, "bus.conf")
	contents := `<busconfig><type>session</type><listen>unix:tmpdir=` + root + `</listen><auth>EXTERNAL</auth><policy context="default"><allow own="*"/><allow send_destination="*"/><allow receive_sender="*"/></policy></busconfig>`
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	daemon := exec.CommandContext(ctx, "dbus-daemon", "--config-file="+config, "--nofork", "--nopidfile", "--print-address=1")
	output, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = daemon.Process.Kill(); _ = daemon.Wait() }()
	address := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		if scanner.Scan() {
			address <- scanner.Text()
		} else {
			address <- ""
		}
	}()
	var bus string
	select {
	case bus = <-address:
	case <-ctx.Done():
		t.Fatal("private bus did not start")
	}
	if bus == "" {
		t.Fatal("private bus has no address")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"available", "missing", "unset"} {
		command := exec.CommandContext(ctx, executable, "-test.run=^TestInstanceBusHelper$")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DBUS_SESSION_BUS_ADDRESS=") && !strings.HasPrefix(value, "WORKSPACE_INSTANCE_BUS_") {
				command.Env = append(command.Env, value)
			}
		}
		address := bus
		if mode != "available" {
			address = "unix:path=" + filepath.Join(root, "missing")
			command.Env = append(command.Env, "WORKSPACE_INSTANCE_BUS_MISSING=1")
		}
		if mode == "unset" {
			address = ""
		}
		command.Env = append(command.Env, "WORKSPACE_INSTANCE_BUS_CHILD=1", "DBUS_SESSION_BUS_ADDRESS="+address)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("private bus check: %v\n%s", err, output)
		}
	}
}
