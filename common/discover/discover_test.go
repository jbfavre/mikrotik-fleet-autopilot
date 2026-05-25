package discover

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
	"jb.favre/mikrotik-fleet-autopilot/common/mndp"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

type testRunner struct {
	runOutput string
	runErr    error
}

func (r *testRunner) Close() error { return nil }

func (r *testRunner) IsAlreadyClosedError(err error) bool { return false }

func (r *testRunner) Run(cmd string) (string, error) {
	return r.runOutput, r.runErr
}

func (r *testRunner) RunInteractive(input string) (string, error) {
	return r.runOutput, r.runErr
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
	})
}

func TestBuildNoHostsConfigured(t *testing.T) {
	_, err := Build(context.Background(), nil, Config{}, Dependencies{
		CreateSSHConnection: func(context.Context, string) (ssh.RunnerInterface, error) {
			return nil, errors.New("must not be called")
		},
		ListenMNDP: func(context.Context, string, time.Duration) ([]*mndp.Device, error) {
			return nil, errors.New("must not be called")
		},
	})
	if err == nil {
		t.Fatal("Build() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no hosts configured for discovery") {
		t.Fatalf("expected no-hosts error, got %v", err)
	}
}

func TestBuildMNDPErrorIsFatal(t *testing.T) {
	_, err := Build(context.Background(), nil, Config{UseMNDP: true, MNDPTimeout: time.Second}, Dependencies{
		CreateSSHConnection: func(context.Context, string) (ssh.RunnerInterface, error) {
			return nil, errors.New("must not be called")
		},
		ListenMNDP: func(context.Context, string, time.Duration) ([]*mndp.Device, error) {
			return nil, errors.New("network unreachable")
		},
	})
	if err == nil {
		t.Fatal("Build() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "mndp discovery failed") {
		t.Fatalf("expected mndp discovery failure, got %v", err)
	}
}

func TestBuildPromotesLLDPNeighborHosts(t *testing.T) {
	createSSHConnection := func(_ context.Context, host string) (ssh.RunnerInterface, error) {
		switch host {
		case "router1":
			return &testRunner{runOutput: `0 interface=ether1 address=192.168.1.2 mac-address=aa:bb:cc:dd:ee:ff identity="router2" discovered-by=lldp`}, nil
		case "router2":
			return &testRunner{runOutput: ""}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	}

	topo, err := Build(context.Background(), []string{"router1"}, Config{}, Dependencies{
		CreateSSHConnection: createSSHConnection,
		ListenMNDP: func(context.Context, string, time.Duration) ([]*mndp.Device, error) {
			return nil, errors.New("must not be called")
		},
	})
	if err != nil {
		t.Fatalf("Build() unexpected error = %v", err)
	}

	if !reflect.DeepEqual(topo.OrderedHosts, []string{"router1", "router2"}) {
		t.Fatalf("OrderedHosts = %v, want [router1 router2]", topo.OrderedHosts)
	}
	if _, ok := topo.LLDPPromoted["router2"]; !ok {
		t.Fatalf("expected router2 to be LLDP-promoted, got %v", topo.LLDPPromoted)
	}
}

func TestBuildRecordsHostErrorsWithoutFailingWholeRun(t *testing.T) {
	createSSHConnection := func(_ context.Context, host string) (ssh.RunnerInterface, error) {
		switch host {
		case "router1":
			return nil, errors.New("dial timeout")
		case "router2":
			return &testRunner{runErr: errors.New("command failed")}, nil
		default:
			return &testRunner{runOutput: ""}, nil
		}
	}

	topo, err := Build(context.Background(), []string{"router1", "router2"}, Config{}, Dependencies{
		CreateSSHConnection: createSSHConnection,
		ListenMNDP: func(context.Context, string, time.Duration) ([]*mndp.Device, error) {
			return nil, errors.New("must not be called")
		},
	})
	if err != nil {
		t.Fatalf("Build() unexpected error = %v", err)
	}
	if len(topo.Errors) != 2 {
		t.Fatalf("expected 2 host errors, got %d", len(topo.Errors))
	}
	if len(topo.Results) != 0 {
		t.Fatalf("expected 0 successful results, got %d", len(topo.Results))
	}
}

func TestTopologyReachableHostsOnlyIncludesSuccessfulHosts(t *testing.T) {
	topo := &Topology{
		OrderedHosts: []string{"router1", "router2", "router3"},
		Results: map[string]*lldp.ParseResult{
			"router1": {},
			"router3": {},
		},
	}

	got := topo.ReachableHosts()
	want := []string{"router1", "router3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReachableHosts() = %v, want %v", got, want)
	}
}

func TestResolveHostsDiscoveryFirstUsesDiscoveredHosts(t *testing.T) {
	hosts, err := ResolveHostsDiscoveryFirst(context.Background(), Config{UseMNDP: true, MNDPTimeout: time.Second}, Dependencies{
		ListenMNDP: func(context.Context, string, time.Duration) ([]*mndp.Device, error) {
			return []*mndp.Device{{Identity: "router.dynamic", MACAddress: "aa:bb:cc:dd:ee:ff"}}, nil
		},
		CreateSSHConnection: func(context.Context, string) (ssh.RunnerInterface, error) {
			return &testRunner{runOutput: ""}, nil
		},
	})
	if err != nil {
		t.Fatalf("ResolveHostsDiscoveryFirst() unexpected error = %v", err)
	}
	if !reflect.DeepEqual(hosts, []string{"router.dynamic"}) {
		t.Fatalf("hosts = %v, want [router.dynamic]", hosts)
	}
}

func TestResolveHostsDiscoveryFirstFallsBackToLocalFiles(t *testing.T) {
	tmpDir := t.TempDir()
	chdirForTest(t, tmpDir)
	if err := os.WriteFile("router-local-1.rsc", []byte("# test"), 0o644); err != nil {
		t.Fatalf("WriteFile(router-local-1.rsc) error = %v", err)
	}
	if err := os.WriteFile("router-local-2.rsc", []byte("# test"), 0o644); err != nil {
		t.Fatalf("WriteFile(router-local-2.rsc) error = %v", err)
	}

	hosts, err := ResolveHostsDiscoveryFirst(context.Background(), Config{UseMNDP: true, MNDPTimeout: time.Second}, Dependencies{
		ListenMNDP: func(context.Context, string, time.Duration) ([]*mndp.Device, error) {
			return []*mndp.Device{{Identity: "router.dynamic", MACAddress: "aa:bb:cc:dd:ee:ff"}}, nil
		},
		CreateSSHConnection: func(context.Context, string) (ssh.RunnerInterface, error) {
			return nil, errors.New("connection failed")
		},
	})
	if err != nil {
		t.Fatalf("ResolveHostsDiscoveryFirst() unexpected error = %v", err)
	}
	if !reflect.DeepEqual(hosts, []string{"router-local-1", "router-local-2"}) {
		t.Fatalf("hosts = %v, want [router-local-1 router-local-2]", hosts)
	}
}

func TestResolveHostsDiscoveryFirstReturnsCombinedErrorWhenBothFail(t *testing.T) {
	tmpDir := t.TempDir()
	chdirForTest(t, tmpDir)

	_, err := ResolveHostsDiscoveryFirst(context.Background(), Config{UseMNDP: true, MNDPTimeout: time.Second}, Dependencies{
		ListenMNDP: func(context.Context, string, time.Duration) ([]*mndp.Device, error) {
			return nil, errors.New("network unreachable")
		},
		CreateSSHConnection: func(context.Context, string) (ssh.RunnerInterface, error) {
			return nil, errors.New("must not be called")
		},
	})
	if err == nil {
		t.Fatal("ResolveHostsDiscoveryFirst() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "topology discovery failed") {
		t.Fatalf("expected discovery failure in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "local router discovery failed") {
		t.Fatalf("expected local fallback failure in error, got %v", err)
	}
}
