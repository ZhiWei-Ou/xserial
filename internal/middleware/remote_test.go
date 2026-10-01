package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
)

type observedContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observedContext) Err() error { c.once.Do(func() { close(c.entered) }); return c.Context.Err() }

func TestRemoteWritesShareKeyboardSerializationAndRecording(t *testing.T) {
	port := newBlockingPort()
	port.shortWrite = 1
	debug := debugsession.New(debugsession.Connection{})
	var journal bytes.Buffer
	recorder, err := capture.NewWriter(&journal, capture.Header{})
	if err != nil {
		t.Fatal(err)
	}
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		id := debug.Status().SessionID
		var workers sync.WaitGroup
		results := make(chan error, 2)
		workers.Go(func() { results <- e.Send(ctx, []byte("keyboard")) })
		workers.Go(func() {
			_, err := debug.Send(ctx, debugsession.SendInput{SessionID: id, Data: "agent"})
			results <- err
		})
		workers.Wait()
		close(results)
		for err := range results {
			if err != nil {
				return err
			}
		}
		if got := port.Written(); got != "keyboardagent" && got != "agentkeyboard" {
			return errors.New("writes interleaved: " + got)
		}
		if debug.Status().TXBytes != 13 {
			return errors.New("TX counters omitted keyboard or short writes")
		}
		e.Quit()
		return nil
	})
	if err := New(Config{Port: port, Debug: debug, Frontend: frontend, Recorder: recorder}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := capture.Read(&journal)
	if err != nil {
		t.Fatal(err)
	}
	var actual []byte
	remote := false
	for _, record := range session.Records {
		if record.Kind == "tx" {
			actual = append(actual, record.Data...)
		}
		if record.Kind == "tx_request" && record.Note == "mcp" {
			remote = true
		}
	}
	if !remote || string(actual) != port.Written() {
		t.Fatalf("recording = %+v", session.Records)
	}
}

func TestConfigurationReplacementRejectsOldIdentityAndWakesRead(t *testing.T) {
	first, second := newBlockingPort(), newBlockingPort()
	debug := debugsession.New(debugsession.Connection{})
	want := ConnectionConfig{PortName: "new", BaudRate: 9600, DataBits: 7, Parity: "even", StopBits: "2"}
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		old := debug.Status()
		readCtx := &observedContext{Context: ctx, entered: make(chan struct{})}
		done := make(chan debugsession.ReadResult, 1)
		errCh := make(chan error, 1)
		wait := 30000
		go func() {
			result, err := debug.Read(readCtx, debugsession.ReadInput{SessionID: old.SessionID, Cursor: old.Cursor, WaitMS: &wait})
			done <- result
			errCh <- err
		}()
		<-readCtx.entered
		configurable := e.(interface {
			Configure(context.Context, ConnectionConfig) error
		})
		if err := configurable.Configure(ctx, want); err != nil {
			return err
		}
		select {
		case result := <-done:
			if err := <-errCh; err != nil {
				return err
			}
			if result.Reason != "detached" {
				return errors.New("read did not detach")
			}
		case <-time.After(time.Second):
			return errors.New("read remained blocked")
		}
		current := debug.Status()
		if current.SessionID == old.SessionID || current.Connection.Port != "new" || current.Connection.Baud != 9600 || current.Connection.Parity != "E" {
			return errors.New("wrong new identity/config")
		}
		if _, err := debug.Send(ctx, debugsession.SendInput{SessionID: old.SessionID, Data: "stale"}); err == nil {
			return errors.New("stale send accepted")
		}
		if _, err := debug.Send(ctx, debugsession.SendInput{SessionID: current.SessionID, Data: "new data"}); err != nil {
			return err
		}
		e.Quit()
		return nil
	})
	if err := New(Config{Port: first, Debug: debug, OpenConnection: func(ConnectionConfig) (SerialPort, error) { return second, nil }, Frontend: frontend}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.Written() != "" || second.Written() != "new data" {
		t.Fatalf("old=%q new=%q", first.Written(), second.Written())
	}
}

func TestReconnectPublishesNewIdentity(t *testing.T) {
	first, second := newBlockingPort(), newBlockingPort()
	debug := debugsession.New(debugsession.Connection{})
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		old := debug.Status().SessionID
		first.Close()
		for event := range e.Events() {
			if _, ok := event.(Reconnected); ok {
				current := debug.Status().SessionID
				if current == "" || current == old {
					return errors.New("reconnect reused identity")
				}
				if _, err := debug.Send(ctx, debugsession.SendInput{SessionID: old, Data: "stale"}); err == nil {
					return errors.New("stale request accepted")
				}
				if _, err := debug.Send(ctx, debugsession.SendInput{SessionID: current, Data: "new"}); err != nil {
					return err
				}
				e.Quit()
				return nil
			}
		}
		return nil
	})
	if err := New(Config{Port: first, Debug: debug, Reconnect: func() (SerialPort, error) { return second, nil }, ReconnectInterval: time.Nanosecond, Frontend: frontend}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second.Written() != "new" {
		t.Fatal(second.Written())
	}
}

func TestYMODEMRejectsRemoteSendWithBusy(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	port := newBlockingPort()
	debug := debugsession.New(debugsession.Connection{})
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		if err := e.StartYMODEMUpload(ctx, file); err != nil {
			return err
		}
		_, err := debug.Send(ctx, debugsession.SendInput{SessionID: debug.Status().SessionID, Data: "agent"})
		var fault *debugsession.Fault
		if !errors.As(err, &fault) || fault.Code != "busy" {
			return errors.New("remote send did not return busy")
		}
		e.CancelTransfer()
		for event := range e.Events() {
			if _, ok := event.(YMODEMFinished); ok {
				e.Quit()
				return nil
			}
		}
		return nil
	})
	if err := New(Config{Port: port, Debug: debug, Frontend: frontend}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if port.Written() == "agent" {
		t.Fatal("remote data entered transfer")
	}
}

type blockedWritePort struct {
	*fakePort
	started chan struct{}
	once    sync.Once
}

func (p *blockedWritePort) Write([]byte) (int, error) {
	p.once.Do(func() { close(p.started) })
	<-p.readDone
	return 0, io.ErrClosedPipe
}

func TestBlockedRemoteWriteTimeoutRetainsUnknownDeliveryAndShutdown(t *testing.T) {
	port := &blockedWritePort{fakePort: newBlockingPort(), started: make(chan struct{})}
	debug := debugsession.New(debugsession.Connection{})
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		status := debug.Status()
		timeout := 10
		result, err := debug.Send(ctx, debugsession.SendInput{SessionID: status.SessionID, Data: "command", TimeoutMS: &timeout})
		if !errors.Is(err, context.DeadlineExceeded) || result.Delivery != "unknown" || result.Cursor == "" {
			return errors.New("write lost unknown delivery/checkpoint")
		}
		select {
		case <-port.started:
		default:
			return errors.New("driver write never started")
		}
		e.Quit()
		return nil
	})
	if err := New(Config{Port: port, Debug: debug, Frontend: frontend}).Run(context.Background()); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

type partialFailurePort struct{ *fakePort }

func (p partialFailurePort) Write(data []byte) (int, error) {
	n, _ := p.fakePort.Write(data[:min(len(data), 2)])
	return n, io.ErrUnexpectedEOF
}

func TestFailedRemoteWriteKeepsCursorAndNeverRetriesPartialData(t *testing.T) {
	port := partialFailurePort{newBlockingPort()}
	debug := debugsession.New(debugsession.Connection{})
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		result, err := debug.Send(ctx, debugsession.SendInput{SessionID: debug.Status().SessionID, Data: "command"})
		if err == nil || result.Delivery != "unknown" || result.Cursor == "" || result.Bytes != 0 {
			return errors.New("partial failure lost unknown delivery")
		}
		e.Quit()
		return nil
	})
	err := New(Config{Port: port, Debug: debug, Frontend: frontend}).Run(context.Background())
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if port.Written() != "co" {
		t.Fatalf("partial write was retried: %q", port.Written())
	}
}
