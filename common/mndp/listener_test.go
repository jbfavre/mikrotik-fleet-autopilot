package mndp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockPacketConn implements net.PacketConn and captures WriteTo calls.
type mockPacketConn struct {
	writtenData []byte
	writtenAddr net.Addr
}

func (m *mockPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	m.writtenData = append([]byte(nil), p...)
	m.writtenAddr = addr
	return len(p), nil
}

func (m *mockPacketConn) ReadFrom(_ []byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}

func (m *mockPacketConn) Close() error {
	return nil
}

func (m *mockPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (m *mockPacketConn) SetDeadline(_ time.Time) error {
	return nil
}

func (m *mockPacketConn) SetReadDeadline(_ time.Time) error {
	return nil
}

func (m *mockPacketConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

// Ensure mockPacketConn satisfies net.PacketConn at compile time.
var _ net.PacketConn = (*mockPacketConn)(nil)

func TestSendProbe_WritesCorrectPayload(t *testing.T) {
	mock := &mockPacketConn{}

	if err := SendProbe(mock); err != nil {
		t.Fatalf("SendProbe() unexpected error = %v", err)
	}

	want := []byte{0x00, 0x00, 0x00, 0x00}
	if len(mock.writtenData) != 4 {
		t.Fatalf("SendProbe() wrote %d bytes, want 4", len(mock.writtenData))
	}
	for i, b := range want {
		if mock.writtenData[i] != b {
			t.Errorf("SendProbe() written[%d] = 0x%02x, want 0x%02x", i, mock.writtenData[i], b)
		}
	}
}

func TestSendProbe_SendsToBroadcast(t *testing.T) {
	mock := &mockPacketConn{}

	if err := SendProbe(mock); err != nil {
		t.Fatalf("SendProbe() unexpected error = %v", err)
	}

	udpAddr, ok := mock.writtenAddr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("SendProbe() destination addr type = %T, want *net.UDPAddr", mock.writtenAddr)
	}
	if !udpAddr.IP.Equal(net.IPv4bcast) {
		t.Errorf("SendProbe() destination IP = %v, want 255.255.255.255", udpAddr.IP)
	}
	if udpAddr.Port != 5678 {
		t.Errorf("SendProbe() destination port = %d, want 5678", udpAddr.Port)
	}
}

func TestDeduplicateDevices_MultiHomedIdentityMergedWithoutDisambiguation(t *testing.T) {
	devByIdentity := map[string]map[string]*Device{
		"router.home": {
			"aa:bb:cc:dd:ee:02": {MACAddress: "aa:bb:cc:dd:ee:02", Identity: "router.home", IPv4Address: "192.168.1.2", InterfaceName: "eth2", SourceInterfaceName: "ether2"},
			"aa:bb:cc:dd:ee:01": {MACAddress: "aa:bb:cc:dd:ee:01", Identity: "router.home", IPv4Address: "192.168.1.1", InterfaceName: "eth1", SourceInterfaceName: "ether1"},
		},
		"switch.home": {
			"aa:bb:cc:dd:ee:03": {MACAddress: "aa:bb:cc:dd:ee:03", Identity: "switch.home", IPv4Address: "192.168.2.1"},
		},
	}

	result := deduplicateDevices(devByIdentity)
	if len(result) != 2 {
		t.Fatalf("deduplicateDevices() returned %d devices, want 2", len(result))
	}
	if result[0].Identity != "router.home" || result[1].Identity != "switch.home" {
		t.Fatalf("deduplicateDevices() got identities %q and %q", result[0].Identity, result[1].Identity)
	}
	if len(result[0].Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces for merged multi-homed device, got %d", len(result[0].Interfaces))
	}
	if result[0].Interfaces[0].MACAddress != "aa:bb:cc:dd:ee:01" || result[0].Interfaces[1].MACAddress != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("expected stable interface ordering by MAC, got %q then %q", result[0].Interfaces[0].MACAddress, result[0].Interfaces[1].MACAddress)
	}
}

func TestAddObservation_MergesSameMACAndAccumulatesIPv4s(t *testing.T) {
	devByIdentity := make(map[string]map[string]*Device)

	first := &Device{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		Identity:      "router.home",
		IPv4Address:   "192.168.1.10",
		IPv4Addresses: []string{"192.168.1.10"},
	}
	second := &Device{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		Identity:      "router.home",
		IPv4Address:   "192.168.1.11",
		IPv4Addresses: []string{"192.168.1.11"},
	}
	duplicate := &Device{
		MACAddress:  "aa:bb:cc:dd:ee:ff",
		Identity:    "router.home",
		IPv4Address: "192.168.1.11",
	}

	addObservation(devByIdentity, first)
	addObservation(devByIdentity, second)
	addObservation(devByIdentity, duplicate)

	result := deduplicateDevices(devByIdentity)
	if len(result) != 1 {
		t.Fatalf("deduplicateDevices() returned %d devices, want 1", len(result))
	}
	if result[0].IPv4Address != "192.168.1.11" {
		t.Fatalf("expected last seen IPv4Address to be retained, got %q", result[0].IPv4Address)
	}
	if len(result[0].IPv4Addresses) != 2 {
		t.Fatalf("expected 2 unique observed IPv4 addresses, got %v", result[0].IPv4Addresses)
	}
	if result[0].IPv4Addresses[0] != "192.168.1.10" || result[0].IPv4Addresses[1] != "192.168.1.11" {
		t.Fatalf("unexpected observed IPv4 order/content: %v", result[0].IPv4Addresses)
	}
	if len(result[0].Interfaces) != 2 {
		t.Fatalf("expected 2 interface records for merged observations, got %d", len(result[0].Interfaces))
	}
}

func TestDeduplicateDevices_WarnsOnMetadataConflict(t *testing.T) {
	devByIdentity := map[string]map[string]*Device{
		"router.home": {
			"aa:bb:cc:dd:ee:01": {MACAddress: "aa:bb:cc:dd:ee:01", Identity: "router.home", Board: "RB5009", Version: "7.18.2", Platform: "MikroTik"},
			"aa:bb:cc:dd:ee:02": {MACAddress: "aa:bb:cc:dd:ee:02", Identity: "router.home", Board: "CCR2116", Version: "7.18.2", Platform: "MikroTik"},
		},
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	deduplicateDevices(devByIdentity)
	if !strings.Contains(logs.String(), "inconsistent metadata across interfaces for identity") {
		t.Fatalf("expected warning log about metadata conflict, got %q", logs.String())
	}
}

func TestListen_RejectsNonPositiveTimeout(t *testing.T) {
	_, err := Listen(context.Background(), "", 0)
	if err == nil {
		t.Fatal("Listen() expected error for non-positive timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be greater than 0") {
		t.Fatalf("Listen() error = %v, want timeout validation error", err)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type mockPacketConnWithResponses struct {
	writeErr           error
	readErr            error
	setReadDeadlineErr error
	blockRead          bool

	mu               sync.Mutex
	packets          [][]byte
	currentPacketIdx int
	readCalls        int
	writeCalls       int
	closed           bool
	lastReadDeadline time.Time
	closeCh          chan struct{}
}

func newMockPacketConnWithResponses(packets ...[]byte) *mockPacketConnWithResponses {
	return &mockPacketConnWithResponses{
		packets: packets,
		closeCh: make(chan struct{}),
	}
}

func (m *mockPacketConnWithResponses) WriteTo(_ []byte, _ net.Addr) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls++
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	return 4, nil
}

func (m *mockPacketConnWithResponses) ReadFrom(p []byte) (int, net.Addr, error) {
	m.mu.Lock()
	m.readCalls++
	blockRead := m.blockRead
	readErr := m.readErr
	if m.currentPacketIdx < len(m.packets) {
		packet := m.packets[m.currentPacketIdx]
		m.currentPacketIdx++
		m.mu.Unlock()
		n := copy(p, packet)
		return n, &net.UDPAddr{IP: net.IPv4(192, 168, 1, 1), Port: 5678}, nil
	}
	ch := m.closeCh
	m.mu.Unlock()

	if blockRead {
		<-ch
		return 0, nil, net.ErrClosed
	}
	if readErr != nil {
		return 0, nil, readErr
	}
	return 0, nil, timeoutError{}
}

func (m *mockPacketConnWithResponses) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	close(m.closeCh)
	return nil
}

func (m *mockPacketConnWithResponses) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4zero, Port: 5678}
}

func (m *mockPacketConnWithResponses) SetDeadline(_ time.Time) error {
	return nil
}

func (m *mockPacketConnWithResponses) SetReadDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastReadDeadline = t
	if m.setReadDeadlineErr != nil {
		return m.setReadDeadlineErr
	}
	return nil
}

func (m *mockPacketConnWithResponses) SetWriteDeadline(_ time.Time) error {
	return nil
}

var _ net.PacketConn = (*mockPacketConnWithResponses)(nil)

func withListenDeps(t *testing.T, ifaceByName func(string) (*net.Interface, error), ifaces func() ([]net.Interface, error), listenPacket func(string, string) (net.PacketConn, error), now func() time.Time) {
	t.Helper()
	oldInterfaceByNameFn := interfaceByNameFn
	oldInterfacesFn := interfacesFn
	oldListenPacketFn := listenPacketFn
	oldTimeNowFn := timeNowFn
	interfaceByNameFn = ifaceByName
	interfacesFn = ifaces
	listenPacketFn = listenPacket
	timeNowFn = now
	t.Cleanup(func() {
		interfaceByNameFn = oldInterfaceByNameFn
		interfacesFn = oldInterfacesFn
		listenPacketFn = oldListenPacketFn
		timeNowFn = oldTimeNowFn
	})
}

func TestAppendUnique(t *testing.T) {
	got := appendUnique(nil, "x")
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("appendUnique(nil, x) = %v", got)
	}

	unchanged := appendUnique([]string{"x"}, "x")
	if len(unchanged) != 1 || unchanged[0] != "x" {
		t.Fatalf("appendUnique duplicate = %v", unchanged)
	}

	noop := appendUnique([]string{"x"}, "")
	if len(noop) != 1 || noop[0] != "x" {
		t.Fatalf("appendUnique empty candidate = %v", noop)
	}

	multi := appendUnique(appendUnique([]string{"a"}, "b"), "c")
	if !slices.Equal(multi, []string{"a", "b", "c"}) {
		t.Fatalf("appendUnique accumulation = %v", multi)
	}
}

func TestMetadataConflict(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  bool
	}{
		{left: "", right: " ", want: false},
		{left: "", right: "x", want: false},
		{left: "x", right: "", want: false},
		{left: "x", right: "x", want: false},
		{left: "x", right: "y", want: true},
	}
	for _, tc := range tests {
		if got := metadataConflict(tc.left, tc.right); got != tc.want {
			t.Fatalf("metadataConflict(%q, %q) = %v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestBuildCandidateInterfaces(t *testing.T) {
	if got := buildCandidateInterfaces(nil); got != nil {
		t.Fatalf("buildCandidateInterfaces(nil) = %v, want nil", got)
	}

	existing := []InterfaceRecord{{MACAddress: "aa"}}
	gotExisting := buildCandidateInterfaces(&Device{Interfaces: existing})
	if !slices.Equal(gotExisting, existing) {
		t.Fatalf("buildCandidateInterfaces existing = %v, want %v", gotExisting, existing)
	}

	if got := buildCandidateInterfaces(&Device{MACAddress: "  "}); got != nil {
		t.Fatalf("buildCandidateInterfaces empty fields = %v, want nil", got)
	}

	got := buildCandidateInterfaces(&Device{MACAddress: "aa", IPv4Address: "10.0.0.1"})
	want := []InterfaceRecord{{MACAddress: "aa", IPv4Address: "10.0.0.1"}}
	if !slices.Equal(got, want) {
		t.Fatalf("buildCandidateInterfaces with MAC+IPv4 = %v, want %v", got, want)
	}
}

func TestMergeIPv4Addresses(t *testing.T) {
	got := mergeIPv4Addresses([]string{"10.0.0.1"}, []string{"10.0.0.1", "10.0.0.2"}, "10.0.0.3")
	want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	if !slices.Equal(got, want) {
		t.Fatalf("mergeIPv4Addresses = %v, want %v", got, want)
	}
}

func TestMergeInterfaces_DeduplicateSortFilterAndAccumulate(t *testing.T) {
	got := mergeInterfaces(nil, []InterfaceRecord{
		{},
		{MACAddress: "bb", IPv4Address: "10.0.0.2", InterfaceName: "eth2", SourceInterfaceName: "ether2"},
		{MACAddress: "aa", IPv4Address: "10.0.0.2", InterfaceName: "eth0", SourceInterfaceName: "ether0"},
		{MACAddress: "aa", IPv4Address: "10.0.0.1", InterfaceName: "eth1", SourceInterfaceName: "ether1"},
		{MACAddress: "aa", IPv4Address: "10.0.0.1", InterfaceName: "eth1", SourceInterfaceName: "ether1"},
	})
	got = mergeInterfaces(got, []InterfaceRecord{{MACAddress: "cc", IPv4Address: "10.0.0.3"}})

	want := []InterfaceRecord{
		{MACAddress: "aa", IPv4Address: "10.0.0.1", InterfaceName: "eth1", SourceInterfaceName: "ether1"},
		{MACAddress: "aa", IPv4Address: "10.0.0.2", InterfaceName: "eth0", SourceInterfaceName: "ether0"},
		{MACAddress: "bb", IPv4Address: "10.0.0.2", InterfaceName: "eth2", SourceInterfaceName: "ether2"},
		{MACAddress: "cc", IPv4Address: "10.0.0.3"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("mergeInterfaces = %#v, want %#v", got, want)
	}
}

func TestAddObservation_IdentityTrimmingAndFirstObservation(t *testing.T) {
	devByIdentity := make(map[string]map[string]*Device)
	addObservation(devByIdentity, &Device{
		MACAddress:          "aa:bb:cc:dd:ee:ff",
		Identity:            "  router.home ",
		IPv4Address:         "10.0.0.1",
		SourceInterfaceName: "ether1",
	})
	addObservation(devByIdentity, &Device{
		MACAddress:          "aa:bb:cc:dd:ee:ff",
		Identity:            "router.home",
		IPv4Address:         "10.0.0.2",
		IPv6Address:         "fe80::1",
		SourceInterfaceName: "ether2",
		InterfaceName:       "eth2",
	})
	got := deduplicateDevices(devByIdentity)
	if len(got) != 1 {
		t.Fatalf("deduplicateDevices len = %d, want 1", len(got))
	}
	if got[0].Identity != "router.home" {
		t.Fatalf("identity = %q, want trimmed", got[0].Identity)
	}
	if !slices.Equal(got[0].IPv4Addresses, []string{"10.0.0.1", "10.0.0.2"}) {
		t.Fatalf("IPv4Addresses = %v", got[0].IPv4Addresses)
	}
	if got[0].IPv4Address != "10.0.0.2" || got[0].IPv6Address != "fe80::1" {
		t.Fatalf("merged addresses IPv4=%q IPv6=%q", got[0].IPv4Address, got[0].IPv6Address)
	}
	if got[0].SourceInterfaceName != "ether2" || got[0].InterfaceName != "eth2" {
		t.Fatalf("interface names not merged: %+v", got[0])
	}
	if len(got[0].Interfaces) < 2 {
		t.Fatalf("expected interface accumulation, got %v", got[0].Interfaces)
	}
}

func TestDeduplicateDevices_EmptyAndSingle(t *testing.T) {
	if got := deduplicateDevices(map[string]map[string]*Device{}); len(got) != 0 {
		t.Fatalf("deduplicateDevices empty = %d, want 0", len(got))
	}
	devByIdentity := map[string]map[string]*Device{
		"router.home": {
			"aa:bb:cc:dd:ee:ff": {MACAddress: "aa:bb:cc:dd:ee:ff", Identity: "router.home"},
		},
	}
	got := deduplicateDevices(devByIdentity)
	if len(got) != 1 || got[0].Identity != "router.home" {
		t.Fatalf("deduplicateDevices single = %+v", got)
	}
}

func TestDeduplicateDevices_MergesIPv4AddressesAcrossMACs(t *testing.T) {
	devByIdentity := map[string]map[string]*Device{
		"router.home": {
			"aa:bb:cc:dd:ee:01": {MACAddress: "aa:bb:cc:dd:ee:01", Identity: "router.home", IPv4Address: "10.0.0.1", IPv4Addresses: []string{"10.0.0.1"}},
			"aa:bb:cc:dd:ee:02": {MACAddress: "aa:bb:cc:dd:ee:02", Identity: "router.home", IPv4Address: "10.0.0.2", IPv4Addresses: []string{"10.0.0.2", "10.0.0.1"}},
		},
	}
	got := deduplicateDevices(devByIdentity)
	if len(got) != 1 {
		t.Fatalf("deduplicateDevices len = %d, want 1", len(got))
	}
	if !slices.Equal(got[0].IPv4Addresses, []string{"10.0.0.1", "10.0.0.2"}) {
		t.Fatalf("IPv4Addresses = %v", got[0].IPv4Addresses)
	}
	if got[0].IPv4Address != "10.0.0.2" {
		t.Fatalf("IPv4Address = %q, want last merged", got[0].IPv4Address)
	}
}

func TestSendProbe_WriteError(t *testing.T) {
	mock := newMockPacketConnWithResponses()
	mock.writeErr = errors.New("write failed")
	err := SendProbe(mock)
	if err == nil || !strings.Contains(err.Error(), "failed to send probe") {
		t.Fatalf("SendProbe() error = %v", err)
	}
}

func TestListen_InterfaceByNameError(t *testing.T) {
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return nil, errors.New("not found") },
		func() ([]net.Interface, error) { return nil, nil },
		func(string, string) (net.PacketConn, error) { return nil, nil },
		time.Now,
	)
	_, err := Listen(context.Background(), "missing0", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "interface \"missing0\" not found") {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestListen_InterfacesError(t *testing.T) {
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return nil, nil },
		func() ([]net.Interface, error) { return nil, errors.New("boom") },
		func(string, string) (net.PacketConn, error) { return nil, nil },
		time.Now,
	)
	_, err := Listen(context.Background(), "", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "failed to list interfaces") {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestListen_ExplicitInterfaceBindError(t *testing.T) {
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil },
		func() ([]net.Interface, error) { return nil, nil },
		func(string, string) (net.PacketConn, error) { return nil, errors.New("bind fail") },
		time.Now,
	)
	_, err := Listen(context.Background(), "eth0", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "failed to bind to interface") {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestListen_AllInterfacesSkipsIneligibleAndBindFailures(t *testing.T) {
	var calls []string
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return nil, nil },
		func() ([]net.Interface, error) {
			return []net.Interface{
				{Name: "lo", Flags: net.FlagLoopback | net.FlagUp},
				{Name: "ptp0", Flags: net.FlagPointToPoint | net.FlagUp},
				{Name: "down0", Flags: 0},
				{Name: "eth0", Flags: net.FlagUp},
			}, nil
		},
		func(_, _ string) (net.PacketConn, error) {
			calls = append(calls, "called")
			return nil, errors.New("bind fail")
		},
		time.Now,
	)
	got, err := Listen(context.Background(), "", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Listen() unexpected error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Listen() devices = %d, want 0", len(got))
	}
	if len(calls) != 1 {
		t.Fatalf("listenPacket called %d times, want 1 for eligible iface only", len(calls))
	}
}

func TestListen_ExplicitInterfaceProbeError(t *testing.T) {
	conn := newMockPacketConnWithResponses()
	conn.writeErr = errors.New("send fail")
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil },
		func() ([]net.Interface, error) { return nil, nil },
		func(string, string) (net.PacketConn, error) { return conn, nil },
		time.Now,
	)
	_, err := Listen(context.Background(), "eth0", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "failed to send probe") {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestListen_SinglePacketAndDeduplication(t *testing.T) {
	packet := buildTestPacket([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, "router.home", "", "", 0, "", "", []byte{10, 0, 0, 1}, nil)
	conn := newMockPacketConnWithResponses(packet)
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil },
		func() ([]net.Interface, error) { return nil, nil },
		func(string, string) (net.PacketConn, error) { return conn, nil },
		time.Now,
	)
	got, err := Listen(context.Background(), "eth0", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Listen() unexpected error = %v", err)
	}
	if len(got) != 1 || got[0].InterfaceName != "eth0" {
		t.Fatalf("Listen() got = %+v", got)
	}
}

func TestListen_MultiplePacketsSameDeviceMergeIPv4s(t *testing.T) {
	p1 := buildTestPacket([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, "router.home", "", "", 0, "", "", []byte{10, 0, 0, 1}, nil)
	p2 := buildTestPacket([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, "router.home", "", "", 0, "", "", []byte{10, 0, 0, 2}, nil)
	conn := newMockPacketConnWithResponses(p1, p2)
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil },
		func() ([]net.Interface, error) { return nil, nil },
		func(string, string) (net.PacketConn, error) { return conn, nil },
		time.Now,
	)
	got, err := Listen(context.Background(), "eth0", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Listen() unexpected error = %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0].IPv4Addresses, []string{"10.0.0.1", "10.0.0.2"}) {
		t.Fatalf("Listen() merged IPv4s = %+v", got)
	}
}

func TestListen_MultipleInterfacesTagInterfaceName(t *testing.T) {
	conn0 := newMockPacketConnWithResponses(buildTestPacket([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}, "router-a", "", "", 0, "", "", []byte{10, 0, 0, 1}, nil))
	conn1 := newMockPacketConnWithResponses(buildTestPacket([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x02}, "router-b", "", "", 0, "", "", []byte{10, 0, 0, 2}, nil))
	i := atomic.Int32{}
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return nil, nil },
		func() ([]net.Interface, error) {
			return []net.Interface{{Name: "eth0", Flags: net.FlagUp}, {Name: "eth1", Flags: net.FlagUp}}, nil
		},
		func(string, string) (net.PacketConn, error) {
			if i.Add(1) == 1 {
				return conn0, nil
			}
			return conn1, nil
		},
		time.Now,
	)
	got, err := Listen(context.Background(), "", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Listen() unexpected error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Listen() len = %d, want 2", len(got))
	}
	if got[0].InterfaceName == got[1].InterfaceName {
		t.Fatalf("expected different interface names, got %+v", got)
	}
}

func TestListen_ContextCancellationDuringRead(t *testing.T) {
	conn := newMockPacketConnWithResponses()
	conn.blockRead = true
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil },
		func() ([]net.Interface, error) { return nil, nil },
		func(string, string) (net.PacketConn, error) { return conn, nil },
		time.Now,
	)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	got, err := Listen(ctx, "eth0", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Listen() unexpected error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no devices on cancel, got %d", len(got))
	}
}

func TestListen_ParseErrorThenValidPacket(t *testing.T) {
	invalid := []byte{0x00, 0x00, 0x00, 0x00}
	valid := buildTestPacket([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, "router.home", "", "", 0, "", "", []byte{10, 0, 0, 9}, nil)
	conn := newMockPacketConnWithResponses(invalid, valid)
	withListenDeps(
		t,
		func(string) (*net.Interface, error) { return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil },
		func() ([]net.Interface, error) { return nil, nil },
		func(string, string) (net.PacketConn, error) { return conn, nil },
		time.Now,
	)
	got, err := Listen(context.Background(), "eth0", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("Listen() unexpected error = %v", err)
	}
	if len(got) != 1 || got[0].Identity != "router.home" {
		t.Fatalf("Listen() got = %+v", got)
	}
}

func TestListen_ReadErrorAndSetDeadlineError(t *testing.T) {
	for name, setup := range map[string]func(*mockPacketConnWithResponses){
		"read-error":         func(c *mockPacketConnWithResponses) { c.readErr = errors.New("read fail") },
		"set-deadline-error": func(c *mockPacketConnWithResponses) { c.setReadDeadlineErr = errors.New("deadline fail") },
	} {
		t.Run(name, func(t *testing.T) {
			conn := newMockPacketConnWithResponses()
			setup(conn)
			withListenDeps(
				t,
				func(string) (*net.Interface, error) { return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil },
				func() ([]net.Interface, error) { return nil, nil },
				func(string, string) (net.PacketConn, error) { return conn, nil },
				time.Now,
			)
			got, err := Listen(context.Background(), "eth0", 20*time.Millisecond)
			if err != nil {
				t.Fatalf("Listen() unexpected error = %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("Listen() got %d devices, want 0", len(got))
			}
		})
	}
}

func TestAddObservation_ConcurrentAccess(t *testing.T) {
	devByIdentity := make(map[string]map[string]*Device)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range 20 {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			addObservation(devByIdentity, &Device{
				MACAddress:  fmt.Sprintf("aa:bb:cc:dd:ee:%02x", i),
				Identity:    "router.home",
				IPv4Address: fmt.Sprintf("10.0.0.%d", i+1),
			})
			mu.Unlock()
		}()
	}
	wg.Wait()
	got := deduplicateDevices(devByIdentity)
	if len(got) != 1 {
		t.Fatalf("deduplicateDevices len = %d, want 1", len(got))
	}
}
