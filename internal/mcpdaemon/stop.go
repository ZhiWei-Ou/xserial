package mcpdaemon

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

func Stop(ctx context.Context, opts Options) error {
	dir, err := StateDirectory(opts.StateDir, opts.Demo)
	if err != nil {
		return err
	}
	opts.StateDir = dir
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	startup, err := waitLock(ctx, filepath.Join(dir, "startup.lock"))
	if err != nil {
		return err
	}
	defer startup.Close()
	client, err := Connect(ctx, opts)
	if err != nil {
		// A crashed daemon may leave endpoint.json behind. The OS lock is the
		// authority; never kill or trust a PID from that stale file.
		lock, lockErr := waitLock(ctx, filepath.Join(dir, "daemon.lock"))
		if lockErr == nil {
			return lock.Close()
		}
		return errors.Join(err, lockErr)
	}
	defer client.Close()
	// Shutdown can close this RPC connection before its response is sent.
	// Acquiring daemon.lock proves all service workers and the port have closed.
	_ = client.Call(ctx, "stop", struct{}{}, nil)
	lock, err := waitLock(ctx, filepath.Join(dir, "daemon.lock"))
	if err != nil {
		return err
	}
	return lock.Close()
}
