package ssh

// SshManager defines the interface for SSH credential management
// This interface is implemented by core.SshManager
type SshManager interface {
	CredentialsProvider
	// String provides a safe string representation (implements fmt.Stringer)
	String() string
}
