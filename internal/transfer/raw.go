package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const DefaultChunkSize = 256

type ProgressFunc func(written, total int64)

func UploadRawFile(ctx context.Context, path string, dst io.Writer, progress ProgressFunc) (int64, error) {
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

	return CopyRaw(ctx, file, dst, DefaultChunkSize, info.Size(), progress)
}

func CopyRaw(ctx context.Context, src io.Reader, dst io.Writer, chunkSize int, total int64, progress ProgressFunc) (int64, error) {
	if chunkSize <= 0 {
		return 0, errors.New("chunk size must be positive")
	}

	buf := make([]byte, chunkSize)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, context.Canceled
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			if err := WriteFull(dst, buf[:n]); err != nil {
				return written, err
			}
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

func WriteFull(dst io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if n > 0 {
			data = data[n:]
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
