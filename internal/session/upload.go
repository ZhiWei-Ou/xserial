package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"
)

const defaultUploadChunkSize = 256

func UploadRawFile(ctx context.Context, path string, serial io.Writer, progress io.Writer) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	return copyRaw(ctx, file, serial, defaultUploadChunkSize, newUploadProgress(info.Size(), progress))
}

type rawProgress interface {
	Add64(num int64) error
	Finish() error
}

func copyRaw(ctx context.Context, src io.Reader, dst io.Writer, chunkSize int, progress rawProgress) (int64, error) {
	if chunkSize <= 0 {
		return 0, errors.New("chunk size must be positive")
	}

	buf := make([]byte, chunkSize)
	var written int64
	for {
		select {
		case <-ctx.Done():
			return written, context.Canceled
		default:
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			if writeErr := writeFull(dst, buf[:n]); writeErr != nil {
				return written, writeErr
			}
			if progress != nil {
				if progressErr := progress.Add64(int64(n)); progressErr != nil {
					return written, progressErr
				}
			}
			written += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if progress != nil {
					if progressErr := progress.Finish(); progressErr != nil {
						return written, progressErr
					}
				}
				return written, nil
			}
			return written, readErr
		}
	}
}

func writeFull(dst io.Writer, buf []byte) error {
	for len(buf) > 0 {
		n, err := dst.Write(buf)
		if n > 0 {
			buf = buf[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func newUploadProgress(size int64, output io.Writer) rawProgress {
	if output == nil {
		return nil
	}

	return progressbar.NewOptions64(
		size,
		progressbar.OptionSetWriter(output),
		progressbar.OptionSetDescription("[[ xserial | TRANSFER ]]"),
		progressbar.OptionSetWidth(24),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowTotalBytes(true),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionOnCompletion(func() {
			fmt.Fprint(output, "\r\n")
		}),
	)
}

func (s *Session) readUploadPath(ctx context.Context) (string, error) {
	printLocal(s.stderr, "\r\n[[ xserial ]] upload file: ")

	var path strings.Builder
	buf := make([]byte, 1)
	for {
		select {
		case <-ctx.Done():
			return "", context.Canceled
		default:
		}

		n, err := s.stdin.Read(buf)
		if n > 0 {
			switch b := buf[0]; b {
			case '\r', '\n':
				printLocal(s.stderr, "\r\n")
				return strings.TrimSpace(path.String()), nil
			case 0x7f, '\b':
				if path.Len() > 0 {
					current := path.String()
					path.Reset()
					path.WriteString(current[:len(current)-1])
					printLocal(s.stderr, "\b \b")
				}
			default:
				path.WriteByte(b)
				_, _ = s.stderr.Write([]byte{b})
			}
		}
		if err != nil {
			return "", err
		}
	}
}
