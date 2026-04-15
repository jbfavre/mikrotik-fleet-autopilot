package ssh

import "errors"

// ErrConnectionFailed marks errors where the router could not be reached over SSH.
// Callers can use errors.Is(err, ErrConnectionFailed) to classify unreachable-host
// scenarios consistently across subcommands.
var ErrConnectionFailed = errors.New("cannot connect to router")
