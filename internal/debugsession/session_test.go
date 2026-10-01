package debugsession

import (
	"context"
	"errors"
	"testing"
)

func TestTerminalHistoryAndIndependentObservers(t *testing.T) {
	s := New(Connection{Port: "test", Baud: 115200})
	s.ConnectionChanged(1, true)
	status := s.Status()
	s.SetSender(func(ctx context.Context, gen uint64, target Status, data []byte, checkpoint func()) error {
		if gen != 1 {
			t.Fatalf("generation = %d", gen)
		}
		checkpoint()
		s.Written(gen, len(data))
		s.Received(gen, []byte("running\r\nprompt> "))
		return nil
	})
	sent, err := s.Send(context.Background(), SendInput{SessionID: status.SessionID, Data: "help\n"})
	if err != nil || sent.Delivery != "written" || sent.Cursor != status.Cursor {
		t.Fatalf("send = %+v, %v", sent, err)
	}
	in := ReadInput{SessionID: status.SessionID, Cursor: sent.Cursor, WaitMS: ptr(0)}
	first, err := s.Read(context.Background(), in)
	if err != nil || first.Output != "running\nprompt> " {
		t.Fatalf("read = %+v, %v", first, err)
	}
	second, err := s.Read(context.Background(), in)
	if err != nil || second != first {
		t.Fatalf("second observer = %+v, %v", second, err)
	}
	if got := s.Status(); got.TXBytes != 5 || got.RXBytes != uint64(first.Bytes) {
		t.Fatalf("status = %+v", got)
	}
}

func TestIdentityReplacementDetachesOldHistory(t *testing.T) {
	s := New(Connection{Port: "test"})
	s.ConnectionChanged(1, true)
	old := s.Status()
	h, _, _, err := s.lookup(old.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s.Received(1, []byte("last output"))
	s.ConnectionChanged(1, false)
	result, err := h.read(context.Background(), ReadInput{Cursor: old.Cursor})
	if err != nil || result.Reason != "detached" || result.Output != "last output" {
		t.Fatalf("detached = %+v, %v", result, err)
	}
	s.ConnectionChanged(2, true)
	if s.Status().SessionID == old.SessionID {
		t.Fatal("identity reused")
	}
	s.Received(1, []byte("old reader"))
	if s.Status().RXBytes != 0 {
		t.Fatal("old data leaked to new history")
	}
	if _, err := s.Send(context.Background(), SendInput{SessionID: old.SessionID, Data: "x"}); err == nil {
		t.Fatal("old send accepted")
	}
	if _, err := s.Read(context.Background(), ReadInput{SessionID: old.SessionID}); err == nil {
		t.Fatal("old read accepted")
	}
}

func TestFailedSendPreservesUnknownDelivery(t *testing.T) {
	s := New(Connection{Port: "test"})
	s.ConnectionChanged(1, true)
	status := s.Status()
	s.SetSender(func(ctx context.Context, gen uint64, target Status, data []byte, checkpoint func()) error {
		s.Received(gen, []byte("queued output"))
		checkpoint()
		return context.DeadlineExceeded
	})
	sent, err := s.Send(context.Background(), SendInput{SessionID: status.SessionID, Data: "command\n"})
	if !errors.Is(err, context.DeadlineExceeded) || sent.Delivery != "unknown" || sent.Cursor == status.Cursor {
		t.Fatalf("send = %+v, %v", sent, err)
	}
}
