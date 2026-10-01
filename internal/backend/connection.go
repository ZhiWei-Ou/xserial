package backend

import "sync"

// The lifecycle owner may close a connection to unblock I/O concurrently with
// shutdown. Each physical port is closed once, regardless of that ordering.
type ownedPort struct {
	Port
	once sync.Once
	err  error
}

func (p *ownedPort) Close() error {
	p.once.Do(func() { p.err = p.Port.Close() })
	return p.err
}
