package rawui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/linetime"
)

func TestChopWriterStreamingLines(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		width             int
	}{
		{"ascii", "123456789\r\nnext\n", "12345\r\nnext\r\n", 5},
		{"unterminated", "123456789", "12345", 5},
		{"colors", "\x1b[31mabcdef\x1b[0m\r\nZ", "\x1b[31mabc\x1b[0m\r\nZ", 3},
		{"wide and combining", "A中e\u0301文ignored\n", "A中e\u0301\r\n", 5},
		{"tab", "a\tbeyond\r\nx", "a    \r\nx", 5},
		{"carriage return", "abcdef\rXY\n", "abc\rXY\r\n", 3},
		{"backspace", "abc\bZ\n", "abc\x1b[3GZ\r\n", 3},
		{"positioning", "abcdef\x1b[1GXY\n", "abc\x1b[1GXY\r\n", 3},
		{"OSC", "\x1b]0;long title\aabcdef", "\x1b]0;long title\aabc", 3},
		{"narrow", "中a\r\nb\n", "\r\nb\r\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunkSize := range []int{1, 2, len(tc.input)} {
				var out bytes.Buffer
				w := newChopWriter(&out, func() (int, error) { return tc.width, nil })
				for remaining := []byte(tc.input); len(remaining) > 0; {
					n := min(chunkSize, len(remaining))
					if written, err := w.Write(remaining[:n]); err != nil || written != n {
						t.Fatalf("Write = %d, %v", written, err)
					}
					remaining = remaining[n:]
				}
				if out.String() != tc.want {
					t.Fatalf("chunk size %d: got %q, want %q", chunkSize, out.String(), tc.want)
				}
			}
		})
	}
}

func TestChopWriterUsesCurrentWidthAndDoesNotRestoreDiscardedText(t *testing.T) {
	var out bytes.Buffer
	width := 5
	w := newChopWriter(&out, func() (int, error) { return width, nil })
	for _, step := range []struct {
		width int
		data  string
	}{
		{5, "abc"}, {3, "def"}, {8, "hidden\n"}, {8, "123456789\n"}, {2, "xyz\n"},
	} {
		width = step.width
		if _, err := io.WriteString(w, step.data); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := out.String(), "abc\r\n12345678\r\nxy\r\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChopWriterIncludesTimestampInWidth(t *testing.T) {
	var out bytes.Buffer
	w := linetime.NewWriter(newChopWriter(&out, func() (int, error) { return 10, nil }), "stamp")
	if _, err := io.WriteString(w, "abcdef\n"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "[stamp] ab\r\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChopLeavesRedirectedOutputUntouched(t *testing.T) {
	var out bytes.Buffer
	w := chopTerminalOutput(&out)
	data := strings.Repeat("x", 200) + "\n"
	if _, err := io.WriteString(w, data); err != nil {
		t.Fatal(err)
	}
	if out.String() != data {
		t.Fatal("redirected bytes changed")
	}
}

func TestChopWriterReportsOutputAndSizeErrors(t *testing.T) {
	failure := errors.New("write failed")
	w := newChopWriter(errorWriter{failure}, func() (int, error) { return 10, nil })
	if _, err := io.WriteString(w, "line"); !errors.Is(err, failure) {
		t.Fatalf("output error = %v", err)
	}
	w = newChopWriter(io.Discard, func() (int, error) { return 0, failure })
	if _, err := io.WriteString(w, "line"); !errors.Is(err, failure) {
		t.Fatalf("size error = %v", err)
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestChopWriterContinuesFullLineAfterWidening(t *testing.T) {
	var out bytes.Buffer
	width := 3
	w := newChopWriter(&out, func() (int, error) { return width, nil })
	_, _ = io.WriteString(w, "abc")
	width = 6
	_, _ = io.WriteString(w, "defg\n")
	if got, want := out.String(), "abc\x1b[4Gdef\r\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
