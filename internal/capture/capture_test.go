package capture

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWireRecordsRoundTripAndExportPreservesRequests(t *testing.T) {
	var output bytes.Buffer
	header := Header{Started: time.Now(), Port: "test", Baud: 115200}
	w, err := NewWriter(&output, header)
	if err != nil {
		t.Fatal(err)
	}
	request, err := w.Append(Record{Kind: "tx_request", Data: []byte{0, 0xFF, 0x1B}})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := w.Append(Record{Kind: "rx", Data: []byte{0xAA, 0}}); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	w.Append(Record{Kind: "tx_complete", Request: request})
	session, err := Read(bytes.NewReader(output.Bytes()))
	if err != nil || len(session.Records) != 22 {
		t.Fatalf("capture = %d records, %v", len(session.Records), err)
	}
	for i, record := range session.Records {
		if record.Seq != uint64(i+1) {
			t.Fatal("records were reordered")
		}
	}
	var subset bytes.Buffer
	if err := Export(&subset, session, "jsonl", Filter{}); err != nil {
		t.Fatal(err)
	}
	copy, err := Read(&subset)
	if err != nil || copy.Records[21].Request != copy.Records[0].Seq {
		t.Fatalf("export lost request identity: %v", err)
	}
	if !bytes.Equal(copy.Records[0].Data, []byte{0, 0xFF, 0x1B}) {
		t.Fatal("binary data changed")
	}
	var text bytes.Buffer
	if err := Export(&text, session, "text", Filter{Match: []byte{0xAA}}); err != nil || strings.Count(text.String(), "rx") != 20 || strings.Contains(text.String(), "tx_request") {
		t.Fatalf("filtered export = %q, %v", text.String(), err)
	}
}

type partialPort struct {
	data   bytes.Buffer
	closed bool
}

func (*partialPort) Read([]byte) (int, error) { return 0, io.EOF }
func (p *partialPort) Write(data []byte) (int, error) {
	n := min(2, len(data))
	p.data.Write(data[:n])
	return n, io.ErrClosedPipe
}
func (p *partialPort) Close() error { p.closed = true; return nil }

func TestCaptureRecordsActualPartialWrite(t *testing.T) {
	var output bytes.Buffer
	w, _ := NewWriter(&output, Header{})
	port := &Port{ReadWriteCloser: &partialPort{}, Recorder: w}
	n, err := port.Write([]byte{1, 2, 3, 4})
	if n != 2 || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write = %d, %v", n, err)
	}
	session, err := Read(&output)
	if err != nil || len(session.Records) != 1 || !bytes.Equal(session.Records[0].Data, []byte{1, 2}) || session.Records[0].Error == "" {
		t.Fatalf("partial write capture = %+v, %v", session, err)
	}
}

type failingWriter struct {
	allowed int
	writes  int
}

func (w *failingWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.writes > w.allowed {
		return 0, io.ErrClosedPipe
	}
	return len(data), nil
}

func TestRecordingFailureIsStickyAndInvalidFilesAreRejected(t *testing.T) {
	dst := &failingWriter{allowed: 1}
	w, err := NewWriter(dst, Header{})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := w.Append(Record{Kind: "rx", Data: []byte{1}}); !errors.Is(err, ErrRecording) || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("record error = %v", err)
		}
	}
	if dst.writes != 2 {
		t.Fatal("writer retried after a persistent recording error")
	}
	for _, input := range []string{"", `{"format":"other","version":1}`, `{"format":"xserial-capture","version":2}`} {
		if _, err := Read(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	var output bytes.Buffer
	good, _ := NewWriter(&output, Header{})
	good.Append(Record{Kind: "rx"})
	if _, err := Read(strings.NewReader(output.String() + `{"seq":2`)); err == nil {
		t.Fatal("truncated record accepted")
	}
}

func TestReplayMarksPersistWithoutChangingRecording(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.xsr.marks.json")
	mark := Mark{At: time.Now(), Note: "CRC mismatch"}
	if err := SaveMark(path, mark); err != nil {
		t.Fatal(err)
	}
	marks, err := LoadMarks(path)
	if err != nil || len(marks) != 1 || marks[0].Note != mark.Note || !marks[0].At.Equal(mark.At) {
		t.Fatalf("marks = %v, %v", marks, err)
	}
}
