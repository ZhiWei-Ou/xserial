package debugsession

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/serialport"
)

type device struct {
	reads        chan []byte
	closed       chan struct{}
	once         sync.Once
	pending      []byte
	mu           sync.Mutex
	writes       []byte
	short        int
	writeErr     error
	eofOnData    bool
	blockedWrite bool
}

func newDevice() *device { return &device{reads: make(chan []byte, 64), closed: make(chan struct{})} }
func (d *device) Read(dst []byte) (int, error) {
	for len(d.pending) == 0 {
		select {
		case d.pending = <-d.reads:
		case <-d.closed:
			return 0, io.EOF
		}
	}
	n := copy(dst, d.pending)
	d.pending = d.pending[n:]
	if d.eofOnData && len(d.pending) == 0 {
		return n, io.EOF
	}
	return n, nil
}
func (d *device) Write(data []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.blockedWrite {
		<-d.closed
		return 0, io.ErrClosedPipe
	}
	n := len(data)
	if d.short > 0 {
		n = min(n, d.short)
	}
	d.writes = append(d.writes, data[:n]...)
	if d.writeErr != nil {
		return n, d.writeErr
	}
	if len(d.writes) > 0 && d.writes[len(d.writes)-1] == '\n' {
		d.reads <- []byte("running\r\n")
		d.reads <- []byte("error: device init failed\r\nroot@board:~# ")
	}
	return n, nil
}
func (d *device) Close() error { d.once.Do(func() { close(d.closed) }); return nil }

func fixture(t *testing.T, d *device) (*Service, *atomic.Int32) {
	t.Helper()
	var opens atomic.Int32
	s := New(context.Background(), Dependencies{
		List: func() ([]serialport.Info, error) { return []serialport.Info{{Name: "test"}}, nil },
		Open: func(context.Context, Connection) (io.ReadWriteCloser, error) { opens.Add(1); return d, nil },
	})
	t.Cleanup(func() {
		if err := s.Shutdown(); err != nil && !errors.Is(err, d.writeErr) {
			t.Error(err)
		}
	})
	return s, &opens
}

func TestDeviceDebuggingSendThenReadAndContinue(t *testing.T) {
	d := newDevice()
	d.short = 2
	s, opens := fixture(t, d)
	ctx := context.Background()
	opened, err := s.Open(ctx, "agent", Connection{Port: "test"})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := s.Send(ctx, "agent", SendInput{SessionID: opened.SessionID, Data: "./app --self-test\n"})
	if err != nil || sent.Delivery != "written" {
		t.Fatalf("send = %+v, %v", sent, err)
	}
	result, err := s.Read(ctx, ReadInput{SessionID: opened.SessionID, Cursor: sent.Cursor, IdleMS: ptr(5)})
	if err != nil || result.Output != "running\nerror: device init failed\nroot@board:~# " {
		t.Fatalf("read = %+v, %v", result, err)
	}
	if result.Reason != "idle" {
		t.Fatalf("reason = %s", result.Reason)
	}
	again, err := s.Read(ctx, ReadInput{SessionID: opened.SessionID, Cursor: sent.Cursor, WaitMS: ptr(0)})
	if err != nil || again.DataBase64 != result.DataBase64 {
		t.Fatalf("repeated = %+v, %v", again, err)
	}
	if status := s.Status("agent"); status.TXBytes != uint64(sent.Bytes) || status.RXBytes != uint64(result.Bytes) {
		t.Fatalf("status = %+v", status)
	}
	if opens.Load() != 1 {
		t.Fatalf("opens = %d", opens.Load())
	}
	closed, err := s.Close(ctx, "agent", SessionInput{opened.SessionID})
	if err != nil || !closed.Closed {
		t.Fatalf("close = %+v, %v", closed, err)
	}
	last, err := s.Read(ctx, ReadInput{SessionID: opened.SessionID, Cursor: result.NextCursor})
	if err != nil || last.Reason != "closed" {
		t.Fatalf("last = %+v, %v", last, err)
	}
	select {
	case <-d.closed:
	default:
		t.Fatal("port was not closed")
	}
}

func TestControlIsExclusiveAndConnectionSurvivesClientRelease(t *testing.T) {
	d := newDevice()
	s, opens := fixture(t, d)
	ctx := context.Background()
	first, err := s.Open(ctx, "first", Connection{Port: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, "second", Connection{Port: "test"}); err == nil {
		t.Fatal("second client acquired control")
	}
	if _, err := s.Send(ctx, "second", SendInput{SessionID: first.SessionID, Data: "bad\n"}); err == nil {
		t.Fatal("observer could send")
	}
	if _, err := s.Open(ctx, "first", Connection{Port: "other"}); err == nil {
		t.Fatal("configuration silently replaced")
	}
	s.Release("first")
	d.reads <- []byte("background log")
	result, err := s.Read(ctx, ReadInput{SessionID: first.SessionID, Cursor: first.Cursor, IdleMS: ptr(0)})
	if err != nil || result.Output != "background log" {
		t.Fatalf("background = %+v, %v", result, err)
	}
	second, err := s.Open(ctx, "second", Connection{Port: "test"})
	if err != nil || !second.Reused || second.SessionID != first.SessionID || opens.Load() != 1 {
		t.Fatalf("reopen = %+v, %v", second, err)
	}
	if status := s.Status("first"); status.Control != "busy" || status.State != "connected" {
		t.Fatalf("status = %+v", status)
	}
}

func TestFailedWriteRetainsCheckpointAndUnknownDelivery(t *testing.T) {
	d := newDevice()
	d.short = 2
	d.writeErr = errors.New("write interrupted")
	s, _ := fixture(t, d)
	opened, err := s.Open(context.Background(), "agent", Connection{Port: "test"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Send(context.Background(), "agent", SendInput{SessionID: opened.SessionID, Data: "dangerous\n"})
	if err == nil || result.Cursor == "" || result.Delivery != "unknown" || result.Bytes != 0 {
		t.Fatalf("send = %+v, %v", result, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if string(d.writes) != "da" {
		t.Fatalf("writes = %q", d.writes)
	}
}

func TestReadCancellationKeepsSerialConnection(t *testing.T) {
	d := newDevice()
	s, _ := fixture(t, d)
	opened, err := s.Open(context.Background(), "agent", Connection{Port: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Read(ctx, ReadInput{SessionID: opened.SessionID, Cursor: opened.Cursor}); !errors.Is(err, context.Canceled) {
		t.Fatalf("read error = %v", err)
	}
	if status := s.Status("agent"); status.State != "connected" || status.Control != "owned" {
		t.Fatalf("status = %+v", status)
	}
	if _, err := s.Send(context.Background(), "agent", SendInput{SessionID: opened.SessionID, Data: "help\n"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceDisconnectRetainsFinalOutput(t *testing.T) {
	d := newDevice()
	d.eofOnData = true
	s, _ := fixture(t, d)
	opened, err := s.Open(context.Background(), "agent", Connection{Port: "test"})
	if err != nil {
		t.Fatal(err)
	}
	d.reads <- []byte("fatal: last diagnostic\r\n")
	result, err := s.Read(context.Background(), ReadInput{SessionID: opened.SessionID, Cursor: opened.Cursor, IdleMS: ptr(1000)})
	if err != nil || result.Output != "fatal: last diagnostic\n" || result.Reason != "disconnected" || result.Error == "" {
		t.Fatalf("final output = %+v, %v", result, err)
	}
}

func TestBlockedWriteReturnsUnknownDeliveryAndCanBeClosed(t *testing.T) {
	d := newDevice()
	d.blockedWrite = true
	s, _ := fixture(t, d)
	opened, err := s.Open(context.Background(), "agent", Connection{Port: "test"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Send(context.Background(), "agent", SendInput{SessionID: opened.SessionID, Data: "command\n", TimeoutMS: ptr(5)})
	if !errors.Is(err, context.DeadlineExceeded) || result.Delivery != "unknown" || result.Cursor == "" {
		t.Fatalf("write = %+v, %v", result, err)
	}
	if _, err := s.Close(context.Background(), "agent", SessionInput{opened.SessionID}); err != nil {
		t.Fatal(err)
	}
}
