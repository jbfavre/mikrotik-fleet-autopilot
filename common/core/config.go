package core

type Config struct {
	Hosts            []string
	User             string
	Debug            bool
	SkipHostKeyCheck bool
}

// GetSkipHostKeyCheck returns the SkipHostKeyCheck flag for host key verification
func (c *Config) GetSkipHostKeyCheck() bool {
	return c.SkipHostKeyCheck
}
