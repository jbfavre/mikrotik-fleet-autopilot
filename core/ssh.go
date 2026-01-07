package core

// SshRunner defines the interface for SSH operations
// This is implemented by ssh.Runner from the ssh package
type SshRunner interface {
	Close() error
	IsAlreadyClosedError(err error) bool
	Run(cmd string) (string, error)
}
