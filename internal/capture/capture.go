// Package capture stores the original wire bytes and session events in versioned JSONL.
package capture

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const Version = 1

var ErrRecording = errors.New("session recording failed")

type Header struct {
	Format   string    `json:"format"`
	Version  int       `json:"version"`
	Started  time.Time `json:"started"`
	Port     string    `json:"port"`
	Baud     int       `json:"baud"`
	DataBits int       `json:"data_bits"`
	Parity   string    `json:"parity"`
	StopBits string    `json:"stop_bits"`
}

type Record struct {
	Seq     uint64    `json:"seq"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Data    []byte    `json:"data,omitempty"`
	Request uint64    `json:"request,omitempty"`
	Error   string    `json:"error,omitempty"`
	Note    string    `json:"note,omitempty"`
}

// Writer serializes records and applies synchronous disk backpressure. There is
// no background queue and no dropped data. A persistent I/O error ends recording
// and is reported to the session owner. The caller owns the destination's Close.
type Writer struct {
	mu      sync.Mutex
	encoder *json.Encoder
	seq     uint64
	err     error
}

func NewWriter(dst io.Writer, header Header) (*Writer, error) {
	header.Format = "xserial-capture"
	header.Version = Version
	if header.Started.IsZero() {
		header.Started = time.Now()
	}
	w := &Writer{encoder: json.NewEncoder(dst)}
	if err := w.encoder.Encode(header); err != nil {
		return nil, fmt.Errorf("%w: write header: %w", ErrRecording, err)
	}
	return w, nil
}

func (w *Writer) Append(record Record) (uint64, error) {
	if w == nil {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	w.seq++
	record.Seq = w.seq
	if record.At.IsZero() {
		record.At = time.Now()
	}
	if err := w.encoder.Encode(record); err != nil {
		w.err = fmt.Errorf("%w: write event: %w", ErrRecording, err)
		return 0, w.err
	}
	return record.Seq, nil
}

func (w *Writer) Err() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Port observes actual Read/Write results before presentation or protocol gates.
// Positive short writes are saved as TX bytes even if the request later fails.
type Port struct {
	io.ReadWriteCloser
	Recorder *Writer
}

func (p *Port) Read(data []byte) (int, error) {
	n, err := p.ReadWriteCloser.Read(data)
	if n > 0 {
		_, recordErr := p.Recorder.Append(Record{Kind: "rx", Data: data[:n], At: time.Now()})
		err = errors.Join(err, recordErr)
	}
	return n, err
}

func (p *Port) Write(data []byte) (int, error) {
	n, err := p.ReadWriteCloser.Write(data)
	if n > 0 {
		record := Record{Kind: "tx", Data: data[:n], At: time.Now()}
		if err != nil {
			record.Error = err.Error()
		}
		_, recordErr := p.Recorder.Append(record)
		err = errors.Join(err, recordErr)
	}
	return n, err
}

type Session struct {
	Header  Header
	Records []Record
}

// Read enforces explicit input limits before a recording is used for replay.
func Read(src io.Reader) (Session, error) {
	var session Session
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 4096), 128<<10)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return session, err
		}
		return session, errors.New("missing capture header")
	}
	if err := json.Unmarshal(scanner.Bytes(), &session.Header); err != nil {
		return session, fmt.Errorf("decode capture header: %w", err)
	}
	if session.Header.Format != "xserial-capture" || session.Header.Version != Version || session.Header.Started.IsZero() {
		return session, errors.New("unsupported or invalid xserial capture header")
	}
	var seq uint64
	bytes := 0
	for scanner.Scan() {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return session, fmt.Errorf("decode capture record %d: %w", len(session.Records)+1, err)
		}
		if record.Seq <= seq || record.At.IsZero() || record.Kind == "" {
			return session, errors.New("invalid capture sequence, time, or event kind")
		}
		seq = record.Seq
		bytes += len(record.Data) + len(record.Note) + len(record.Error)
		if bytes > 128<<20 || len(session.Records) >= 1000000 {
			return session, errors.New("capture exceeds replay limit of 128 MiB or 1000000 records")
		}
		session.Records = append(session.Records, record)
	}
	if err := scanner.Err(); err != nil {
		return session, fmt.Errorf("read capture: %w", err)
	}
	return session, nil
}

func Load(path string) (Session, error) {
	file, err := os.Open(path)
	if err != nil {
		return Session{}, fmt.Errorf("open capture: %w", err)
	}
	defer file.Close()
	return Read(file)
}
