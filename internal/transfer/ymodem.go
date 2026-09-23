package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ymodemSOH byte = 0x01
	ymodemSTX byte = 0x02
	ymodemEOT byte = 0x04
	ymodemACK byte = 0x06
	ymodemNAK byte = 0x15
	ymodemCAN byte = 0x18
	ymodemCRC byte = 'C'

	ymodemShortBlock = 128
	ymodemLongBlock  = 1024
	ymodemRetries    = 10
)

var (
	ErrYMODEMRemoteCanceled = errors.New("YMODEM canceled by remote")
	ErrYMODEMProtocol       = errors.New("YMODEM protocol error")
)

type ymodemPacket struct {
	number byte
	data   []byte
}

type YMODEMStats struct {
	Bytes         int64
	CRC32         uint32
	FailedFrames  int
	RetriedFrames int
}

type YMODEMRetry struct {
	Path    string
	Block   byte
	Attempt int
	Reason  string
}

type YMODEMRetryFunc func(YMODEMRetry)

type ProgressFunc func(written, total int64)

func SendYMODEMFile(ctx context.Context, path string, conn io.ReadWriter, progress ProgressFunc, retry YMODEMRetryFunc) (stats YMODEMStats, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return stats, err
	}
	if !info.Mode().IsRegular() {
		return stats, fmt.Errorf("%s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer file.Close()

	if err := waitYMODEMByte(ctx, conn, ymodemCRC); err != nil {
		return stats, fmt.Errorf("wait for receiver: %w", err)
	}
	header := make([]byte, ymodemShortBlock)
	metadata := filepath.Base(path) + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00"
	if len(metadata) > len(header) {
		return stats, errors.New("YMODEM filename is too long")
	}
	copy(header, metadata)
	if err := sendYMODEMPacket(ctx, conn, 0, header, &stats, retry); err != nil {
		return stats, fmt.Errorf("send YMODEM header: %w", err)
	}
	if err := waitYMODEMByte(ctx, conn, ymodemCRC); err != nil {
		return stats, fmt.Errorf("wait to send YMODEM data: %w", err)
	}

	buf := make([]byte, ymodemLongBlock)
	checksum := crc32.NewIEEE()
	var block byte = 1
	for {
		if err := ctx.Err(); err != nil {
			stats.CRC32 = checksum.Sum32()
			return stats, context.Canceled
		}
		n, readErr := io.ReadFull(file, buf)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			stats.CRC32 = checksum.Sum32()
			return stats, readErr
		}
		packet := make([]byte, ymodemLongBlock)
		copy(packet, buf[:n])
		for i := n; i < len(packet); i++ {
			packet[i] = 0x1a
		}
		if err := sendYMODEMPacket(ctx, conn, block, packet, &stats, retry); err != nil {
			stats.CRC32 = checksum.Sum32()
			return stats, fmt.Errorf("send YMODEM block %d: %w", block, err)
		}
		_, _ = checksum.Write(buf[:n])
		stats.Bytes += int64(n)
		if progress != nil {
			progress(stats.Bytes, info.Size())
		}
		block++
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
	}
	stats.CRC32 = checksum.Sum32()

	if err := writeYMODEM(ctx, conn, []byte{ymodemEOT}); err != nil {
		return stats, err
	}
	if err := waitYMODEMByte(ctx, conn, ymodemNAK); err != nil {
		return stats, fmt.Errorf("finish YMODEM transfer: %w", err)
	}
	if err := writeYMODEM(ctx, conn, []byte{ymodemEOT}); err != nil {
		return stats, err
	}
	if err := waitYMODEMByte(ctx, conn, ymodemACK); err != nil {
		return stats, fmt.Errorf("finish YMODEM transfer: %w", err)
	}
	if err := waitYMODEMByte(ctx, conn, ymodemCRC); err != nil {
		return stats, fmt.Errorf("finish YMODEM batch: %w", err)
	}
	if err := sendYMODEMPacket(ctx, conn, 0, make([]byte, ymodemShortBlock), &stats, retry); err != nil {
		return stats, fmt.Errorf("finish YMODEM batch: %w", err)
	}
	return stats, nil
}

func ReceiveYMODEMFile(ctx context.Context, dir string, conn io.ReadWriter, progress ProgressFunc, retry YMODEMRetryFunc) (path string, stats YMODEMStats, err error) {
	if dir == "" {
		dir = "."
	}
	if err := writeYMODEM(ctx, conn, []byte{ymodemCRC}); err != nil {
		return "", stats, err
	}
	header, err := receiveYMODEMPacket(ctx, conn)
	if err != nil {
		return "", stats, fmt.Errorf("receive YMODEM header: %w", err)
	}
	name, size, err := parseYMODEMHeader(header.data)
	if err != nil {
		return "", stats, err
	}
	path = filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", stats, fmt.Errorf("create download %q: %w", path, err)
	}
	completed := false
	defer func() {
		closeErr := file.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
		if !completed {
			_ = os.Remove(path)
		}
	}()

	if err = writeYMODEM(ctx, conn, []byte{ymodemACK, ymodemCRC}); err != nil {
		return path, stats, err
	}
	checksum := crc32.NewIEEE()
	expected := byte(1)
	retryAttempt := 1
	for stats.Bytes < size {
		packet, packetErr := receiveYMODEMPacket(ctx, conn)
		if packetErr != nil {
			if errors.Is(packetErr, errYMODEMChecksum) {
				stats.FailedFrames++
				stats.RetriedFrames++
				retryAttempt++
				if retry != nil {
					retry(YMODEMRetry{Path: path, Block: packet.number, Attempt: retryAttempt, Reason: "CRC mismatch"})
				}
				if err = writeYMODEM(ctx, conn, []byte{ymodemNAK}); err != nil {
					return path, stats, err
				}
				continue
			}
			return path, stats, packetErr
		}
		if packet.number != expected {
			return path, stats, fmt.Errorf("%w: got block %d, want %d", ErrYMODEMProtocol, packet.number, expected)
		}
		count := int64(len(packet.data))
		if remaining := size - stats.Bytes; count > remaining {
			count = remaining
		}
		if _, err = file.Write(packet.data[:count]); err != nil {
			return path, stats, err
		}
		_, _ = checksum.Write(packet.data[:count])
		stats.Bytes += count
		expected++
		retryAttempt = 1
		if progress != nil {
			progress(stats.Bytes, size)
		}
		if err = writeYMODEM(ctx, conn, []byte{ymodemACK}); err != nil {
			return path, stats, err
		}
	}
	stats.CRC32 = checksum.Sum32()

	if err = waitYMODEMByte(ctx, conn, ymodemEOT); err != nil {
		return path, stats, fmt.Errorf("wait for YMODEM end: %w", err)
	}
	if err = writeYMODEM(ctx, conn, []byte{ymodemNAK}); err != nil {
		return path, stats, err
	}
	if err = waitYMODEMByte(ctx, conn, ymodemEOT); err != nil {
		return path, stats, fmt.Errorf("wait for YMODEM end: %w", err)
	}
	if err = writeYMODEM(ctx, conn, []byte{ymodemACK, ymodemCRC}); err != nil {
		return path, stats, err
	}
	end, err := receiveYMODEMPacket(ctx, conn)
	if err != nil {
		return path, stats, fmt.Errorf("receive YMODEM batch end: %w", err)
	}
	if len(bytes.Trim(end.data, "\x00")) != 0 {
		return path, stats, errors.New("YMODEM batch contains more than one file")
	}
	if err = writeYMODEM(ctx, conn, []byte{ymodemACK}); err != nil {
		return path, stats, err
	}
	completed = true
	return path, stats, nil
}

var errYMODEMChecksum = errors.New("YMODEM checksum mismatch")

func sendYMODEMPacket(ctx context.Context, conn io.ReadWriter, number byte, data []byte, stats *YMODEMStats, retry YMODEMRetryFunc) error {
	marker := ymodemSOH
	if len(data) == ymodemLongBlock {
		marker = ymodemSTX
	} else if len(data) != ymodemShortBlock {
		return errors.New("invalid YMODEM block size")
	}
	crc := crc16XMODEM(data)
	packet := make([]byte, 0, len(data)+5)
	packet = append(packet, marker, number, ^number)
	packet = append(packet, data...)
	packet = append(packet, byte(crc>>8), byte(crc))
	for attempt := 1; attempt <= ymodemRetries; attempt++ {
		if err := writeYMODEM(ctx, conn, packet); err != nil {
			return err
		}
		response, err := readYMODEMByte(ctx, conn)
		if err != nil {
			return err
		}
		switch response {
		case ymodemACK:
			return nil
		case ymodemNAK:
			stats.FailedFrames++
			if attempt < ymodemRetries {
				stats.RetriedFrames++
				if retry != nil {
					retry(YMODEMRetry{Block: number, Attempt: attempt + 1, Reason: "NAK"})
				}
			}
			continue
		case ymodemCAN:
			return ErrYMODEMRemoteCanceled
		default:
			return fmt.Errorf("%w: unexpected response 0x%02x", ErrYMODEMProtocol, response)
		}
	}
	return errors.New("YMODEM retry limit exceeded")
}

func receiveYMODEMPacket(ctx context.Context, conn io.Reader) (ymodemPacket, error) {
	marker, err := readYMODEMByte(ctx, conn)
	if err != nil {
		return ymodemPacket{}, err
	}
	if marker == ymodemCAN {
		return ymodemPacket{}, ErrYMODEMRemoteCanceled
	}
	size := 0
	switch marker {
	case ymodemSOH:
		size = ymodemShortBlock
	case ymodemSTX:
		size = ymodemLongBlock
	default:
		return ymodemPacket{}, fmt.Errorf("%w: unexpected packet marker 0x%02x", ErrYMODEMProtocol, marker)
	}
	packet := make([]byte, size+4)
	if err := readYMODEMFull(ctx, conn, packet); err != nil {
		return ymodemPacket{}, err
	}
	if packet[0] != ^packet[1] {
		return ymodemPacket{}, fmt.Errorf("%w: invalid block number", ErrYMODEMProtocol)
	}
	data := packet[2 : 2+size]
	wantCRC := uint16(packet[len(packet)-2])<<8 | uint16(packet[len(packet)-1])
	if crc16XMODEM(data) != wantCRC {
		return ymodemPacket{number: packet[0]}, errYMODEMChecksum
	}
	return ymodemPacket{number: packet[0], data: data}, nil
}

func parseYMODEMHeader(data []byte) (string, int64, error) {
	nameEnd := bytes.IndexByte(data, 0)
	if nameEnd <= 0 {
		return "", 0, errors.New("invalid YMODEM filename")
	}
	name := string(data[:nameEnd])
	if filepath.Base(name) != name || name == "." || name == ".." {
		return "", 0, errors.New("unsafe YMODEM filename")
	}
	sizeField := data[nameEnd+1:]
	if end := bytes.IndexByte(sizeField, 0); end >= 0 {
		sizeField = sizeField[:end]
	}
	sizeText := strings.Fields(string(sizeField))
	if len(sizeText) == 0 {
		return "", 0, errors.New("missing YMODEM file size")
	}
	size, err := strconv.ParseInt(sizeText[0], 10, 64)
	if err != nil || size < 0 {
		return "", 0, errors.New("invalid YMODEM file size")
	}
	return name, size, nil
}

func waitYMODEMByte(ctx context.Context, conn io.Reader, want byte) error {
	got, err := readYMODEMByte(ctx, conn)
	if err != nil {
		return err
	}
	if got == ymodemCAN {
		return ErrYMODEMRemoteCanceled
	}
	if got != want {
		return fmt.Errorf("%w: got 0x%02x, want 0x%02x", ErrYMODEMProtocol, got, want)
	}
	return nil
}

func readYMODEMByte(ctx context.Context, src io.Reader) (byte, error) {
	buf := []byte{0}
	if err := readYMODEMFull(ctx, src, buf); err != nil {
		return 0, err
	}
	return buf[0], nil
}

func readYMODEMFull(ctx context.Context, src io.Reader, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return context.Canceled
		}
		n, err := src.Read(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

func writeYMODEM(ctx context.Context, dst io.Writer, data []byte) error {
	if err := ctx.Err(); err != nil {
		return context.Canceled
	}
	return WriteFull(dst, data)
}

func crc16XMODEM(data []byte) uint16 {
	var crc uint16
	for _, value := range data {
		crc ^= uint16(value) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
