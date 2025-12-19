package core

type Config struct {
	Hosts            []string
	User             string
	Debug            bool
	SkipHostKeyCheck bool
}
