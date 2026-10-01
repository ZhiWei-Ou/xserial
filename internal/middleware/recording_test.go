package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
)

func TestSessionRecordsRequestActualShortWritesAndCompletion(t *testing.T) {
	port := newBlockingPort()
	port.shortWrite = 2
	var output bytes.Buffer
	recorder, err := capture.NewWriter(&output, capture.Header{})
	if err != nil {
		t.Fatal(err)
	}
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		if err := e.Send(ctx, []byte("abcdef")); err != nil {
			return err
		}
		e.Quit()
		return nil
	})
	if err := New(Config{Port: port, Frontend: frontend, Recorder: recorder}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := capture.Read(&output)
	if err != nil {
		t.Fatal(err)
	}
	var requested, actual []byte
	complete := false
	for _, record := range session.Records {
		switch record.Kind {
		case "tx_request":
			requested = append(requested, record.Data...)
		case "tx":
			actual = append(actual, record.Data...)
		case "tx_complete":
			complete = true
		}
	}
	if string(requested) != "abcdef" || string(actual) != "abcdef" || !complete || session.Records[len(session.Records)-1].Kind != "closed" {
		t.Fatalf("incomplete journal: %+v", session.Records)
	}
}

type recordingErrorWriter struct{ calls int }

func (w *recordingErrorWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.calls >= 4 {
		return 0, io.ErrClosedPipe
	}
	return len(data), nil
}

func TestDiskFailureEndsSessionAndClosesSerialPort(t *testing.T) {
	port := newBlockingPort()
	recorder, err := capture.NewWriter(&recordingErrorWriter{}, capture.Header{})
	if err != nil {
		t.Fatal(err)
	}
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error { return e.Send(ctx, []byte{1, 2, 3}) })
	err = New(Config{Port: port, Frontend: frontend, Recorder: recorder}).Run(context.Background())
	if !errors.Is(err, capture.ErrRecording) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("recording failure = %v", err)
	}
	select {
	case <-port.readDone:
	default:
		t.Fatal("port remained open after disk failure")
	}
}
