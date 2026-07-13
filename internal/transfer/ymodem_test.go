package transfer

import (
	"bytes"
	"context"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestYMODEMSendsAndReceivesFile(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	source := filepath.Join(sourceDir, "firmware.bin")
	want := bytes.Repeat([]byte("ymodem-data-"), 150)
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatal(err)
	}

	sender, receiver := net.Pipe()
	defer sender.Close()
	defer receiver.Close()
	type result struct {
		path  string
		stats YMODEMStats
		err   error
	}
	received := make(chan result, 1)
	go func() {
		path, stats, err := ReceiveYMODEMFile(context.Background(), destinationDir, receiver, nil, nil)
		received <- result{path: path, stats: stats, err: err}
	}()

	sent, err := SendYMODEMFile(context.Background(), source, sender, nil, nil)
	if err != nil {
		t.Fatalf("SendYMODEMFile() error = %v", err)
	}
	gotResult := <-received
	if gotResult.err != nil {
		t.Fatalf("ReceiveYMODEMFile() error = %v", gotResult.err)
	}
	if sent.Bytes != int64(len(want)) || gotResult.stats.Bytes != int64(len(want)) {
		t.Fatalf("sent=%d received=%d want=%d", sent.Bytes, gotResult.stats.Bytes, len(want))
	}
	wantCRC := crc32.ChecksumIEEE(want)
	if sent.CRC32 != wantCRC || gotResult.stats.CRC32 != wantCRC {
		t.Fatalf("send CRC32=%08x receive CRC32=%08x want=%08x", sent.CRC32, gotResult.stats.CRC32, wantCRC)
	}
	got, err := os.ReadFile(gotResult.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("downloaded file does not match source")
	}
}

func TestYMODEMSendReportsRejectedAndRetriedFrame(t *testing.T) {
	file := filepath.Join(t.TempDir(), "firmware.bin")
	data := []byte("firmware")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	conn := &scriptedYMODEMConn{responses: bytes.NewReader([]byte{
		ymodemCRC,
		ymodemACK, ymodemCRC,
		ymodemNAK, ymodemACK,
		ymodemNAK, ymodemACK, ymodemCRC,
		ymodemACK,
	})}
	var retries []YMODEMRetry

	stats, err := SendYMODEMFile(context.Background(), file, conn, nil, func(retry YMODEMRetry) {
		retries = append(retries, retry)
	})
	if err != nil {
		t.Fatalf("SendYMODEMFile() error = %v", err)
	}
	if stats.FailedFrames != 1 || stats.RetriedFrames != 1 {
		t.Fatalf("failed=%d retried=%d, want 1/1", stats.FailedFrames, stats.RetriedFrames)
	}
	if len(retries) != 1 || retries[0].Block != 1 || retries[0].Attempt != 2 {
		t.Fatalf("retry events = %#v", retries)
	}
	if stats.CRC32 != crc32.ChecksumIEEE(data) {
		t.Fatalf("CRC32 = %08x", stats.CRC32)
	}
}

func TestYMODEMReceiveReportsChecksumFailureAndRetry(t *testing.T) {
	data := []byte("firmware")
	header := make([]byte, ymodemShortBlock)
	copy(header, "firmware.bin\x008\x00")
	payload := bytes.Repeat([]byte{0x1a}, ymodemLongBlock)
	copy(payload, data)
	badPacket := encodeTestYMODEMPacket(1, payload)
	badPacket[len(badPacket)-1] ^= 0xff
	stream := bytes.NewBuffer(nil)
	stream.Write(encodeTestYMODEMPacket(0, header))
	stream.Write(badPacket)
	stream.Write(encodeTestYMODEMPacket(1, payload))
	stream.WriteByte(ymodemEOT)
	stream.WriteByte(ymodemEOT)
	stream.Write(encodeTestYMODEMPacket(0, make([]byte, ymodemShortBlock)))
	conn := &bufferedYMODEMConn{reader: stream}
	var retries []YMODEMRetry

	path, stats, err := ReceiveYMODEMFile(context.Background(), t.TempDir(), conn, nil, func(retry YMODEMRetry) {
		retries = append(retries, retry)
	})
	if err != nil {
		t.Fatalf("ReceiveYMODEMFile() error = %v", err)
	}
	if filepath.Base(path) != "firmware.bin" || stats.FailedFrames != 1 || stats.RetriedFrames != 1 {
		t.Fatalf("path=%q failed=%d retried=%d", path, stats.FailedFrames, stats.RetriedFrames)
	}
	if len(retries) != 1 || retries[0].Block != 1 || retries[0].Attempt != 2 {
		t.Fatalf("retry events = %#v", retries)
	}
	if stats.CRC32 != crc32.ChecksumIEEE(data) {
		t.Fatalf("CRC32 = %08x", stats.CRC32)
	}
}

type bufferedYMODEMConn struct {
	reader io.Reader
	writes bytes.Buffer
}

func (c *bufferedYMODEMConn) Read(data []byte) (int, error)  { return c.reader.Read(data) }
func (c *bufferedYMODEMConn) Write(data []byte) (int, error) { return c.writes.Write(data) }

func encodeTestYMODEMPacket(number byte, data []byte) []byte {
	marker := ymodemSOH
	if len(data) == ymodemLongBlock {
		marker = ymodemSTX
	}
	crc := crc16XMODEM(data)
	packet := []byte{marker, number, ^number}
	packet = append(packet, data...)
	return append(packet, byte(crc>>8), byte(crc))
}

type scriptedYMODEMConn struct {
	responses *bytes.Reader
}

func (c *scriptedYMODEMConn) Read(data []byte) (int, error)  { return c.responses.Read(data) }
func (c *scriptedYMODEMConn) Write(data []byte) (int, error) { return io.Discard.Write(data) }

func TestReceiveYMODEMRejectsUnsafeFilename(t *testing.T) {
	header := make([]byte, ymodemShortBlock)
	copy(header, "../firmware.bin\x001\x00")
	if _, _, err := parseYMODEMHeader(header); err == nil {
		t.Fatal("parseYMODEMHeader() error = nil")
	}
}
