package rawui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
)

func TestStatusBarLifecycleAndResize(t *testing.T) {
	var out bytes.Buffer
	width, height := 80, 24
	b := &StatusBar{dst: &out, parser: ansi.NewParser(), size: func() (int, int, error) { return width, height, nil }}
	cfg := session.ConnectionConfig{PortName: "/dev/cu.usbserial", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}
	if err := b.start(cfg); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "\x1b[1;23r") || !strings.Contains(got, "115200 · 8N1") || strings.Contains(got, "1049") {
		t.Fatalf("initial screen: %q", got)
	}
	b.update("Reconnecting", nil, time.Time{})
	out.Reset()
	if _, err := b.refresh(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Reconnecting") {
		t.Fatal("missing connection state")
	}
	width, height = 60, 15
	out.Reset()
	resized, err := b.refresh()
	if err != nil || !resized || !strings.Contains(out.String(), "\x1b[1;14r") {
		t.Fatalf("resize=%v err=%v screen=%q", resized, err, out.String())
	}
	out.Reset()
	if err := b.close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "\x1b[r") || !strings.Contains(got, "\x1b[15;1H\x1b[2K") {
		t.Fatalf("restore: %q", got)
	}

}

func TestStatusBarLayoutAndTransfer(t *testing.T) {
	now := time.Now()
	b := &StatusBar{state: "Connected", cfg: session.ConnectionConfig{PortName: "/dev/cu.usbserial-long-device-name", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}, progress: &session.YMODEMProgress{Written: 1024, Total: 2048}, started: now.Add(-time.Second)}
	for _, width := range []int{120, 80, 60, 40, 20} {
		b.width = width
		line := ansi.Strip(b.view(now))
		if ansi.StringWidth(line) >= width {
			t.Fatalf("width %d: %q", width, line)
		}
		if width >= 60 && (!strings.Contains(line, "50%") || !strings.Contains(line, "115200")) {
			t.Fatalf("width %d loses transfer/config: %q", width, line)
		}
		t.Logf("%d: %s", width, line)
	}
}

func TestStatusBarDoesNotSplitEscapeOrUTF8(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	b := &StatusBar{dst: file, parser: ansi.NewParser(), active: true, width: 80, height: 24, state: "Connected"}
	w := &statusWriter{bar: b, file: file}
	for _, pair := range [][2]string{{"\x1b[", "31mred"}, {"\xe5", "\x9b\xba"}, {"\x1b]0;title", "\a"}} {
		start, _ := file.Seek(0, io.SeekCurrent)
		if _, err := w.Write([]byte(pair[0])); err != nil {
			t.Fatal(err)
		}
		pos, _ := file.Seek(0, io.SeekCurrent)
		if pos-start != int64(len(pair[0])) {
			t.Fatal("status bytes inserted inside partial sequence")
		}
		if _, err := w.Write([]byte(pair[1])); err != nil {
			t.Fatal(err)
		}
		pos, _ = file.Seek(0, io.SeekCurrent)
		data := make([]byte, pos-start)
		if _, err := file.ReadAt(data, start); err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(data, []byte(pair[0]+pair[1]+"\x1b7")) {
			t.Fatalf("device sequence changed: %q", data)
		}
	}
}

func TestStatusBarDisabledForRedirectedOutput(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	local, err := os.CreateTemp(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	b := NewStatusBar(output, local)
	if err := b.start(session.ConnectionConfig{}); err != nil {
		t.Fatal(err)
	}
	if b.enabled() {
		t.Fatal("enabled on redirected output")
	}
	want := []byte("\x1b[31mdevice\r\n")
	if _, err := b.Output.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := b.close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output.Name())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("output %q error %v", got, err)
	}
	got, err = os.ReadFile(local.Name())
	if err != nil || len(got) != 0 {
		t.Fatalf("local %q error %v", got, err)
	}
}
