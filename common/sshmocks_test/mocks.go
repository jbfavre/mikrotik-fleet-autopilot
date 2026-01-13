// common/ssh/testing/mocks.go
package sshmocks_test

import (
	"context"
	"fmt"

	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// MockRunner implements ssh.RunnerInterface for testing
type MockRunner struct {
	CloseFunc                func() error
	IsAlreadyClosedErrorFunc func(err error) bool
	RunFunc                  func(cmd string) (string, error)
	CommandHistory           []string // Make this exportable for all tests
}

func (m *MockRunner) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

func (m *MockRunner) IsAlreadyClosedError(err error) bool {
	if m.IsAlreadyClosedErrorFunc != nil {
		return m.IsAlreadyClosedErrorFunc(err)
	}
	return false
}

func (m *MockRunner) Run(cmd string) (string, error) {
	m.CommandHistory = append(m.CommandHistory, cmd)
	if m.RunFunc != nil {
		return m.RunFunc(cmd)
	}
	return "", nil
}

// MockManager implements ssh manager for testing
type MockManager struct {
	CreateConnectionFunc func(ctx context.Context, host string) (ssh.RunnerInterface, error)
	GetUserFunc          func() string
}

func (m *MockManager) CreateConnection(ctx context.Context, host string) (ssh.RunnerInterface, error) {
	if m.CreateConnectionFunc != nil {
		return m.CreateConnectionFunc(ctx, host)
	}
	return nil, fmt.Errorf("mock CreateConnection not implemented")
}

func (m *MockManager) GetUser() string {
	if m.GetUserFunc != nil {
		return m.GetUserFunc()
	}
	return "admin"
}
