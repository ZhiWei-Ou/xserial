package rawui

import (
	"bytes"
	"context"
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
	if got := out.String(); !strings.Contains(got, "\x1b[1;23r") || !strings.Contains(got, "115200/8N1") || strings.Contains(got, "1049") {
		t.Fatalf("initial screen: %q", got)
	}
	if got := out.String(); !strings.HasPrefix(got, "\x1b7\x1b[r\x1b8\x1bD\x1bM\x1b7\x1b[1;23r\x1b8") {
		t.Fatalf("startup must preserve the content cursor: %q", got)
	}
	b.update("Reconnecting", inputNormal)
	out.Reset()
	if _, err := b.refresh(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "* [retry]") {
		t.Fatal("missing connection state")
	}
	width, height = 60, 15
	out.Reset()
	resized, err := b.refresh()
	if err != nil || !resized || !strings.Contains(out.String(), "\x1b[1;14r") {
		t.Fatalf("resize=%v err=%v screen=%q", resized, err, out.String())
	}
	if strings.Contains(out.String(), "\x1b[14;1H") || !strings.Contains(out.String(), "\x1b[1;14r\x1b8") {
		t.Fatalf("resize must restore the content cursor: %q", out.String())
	}
	out.Reset()
	if err := b.close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "\x1b[r") || !strings.Contains(got, "\x1b[15;1H\x1b[2K") || !strings.HasSuffix(got, "\x1b8") {
		t.Fatalf("restore: %q", got)
	}

}

func TestStatusBarLayoutAndTips(t *testing.T) {
	now := time.Now()
	b := &StatusBar{state: "Connected", cfg: session.ConnectionConfig{PortName: "/dev/cu.usbserial-long-device-name", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}, mode: inputYMODEMTransfer}
	for _, width := range []int{120, 80, 60, 40, 20} {
		b.width = width
		line := ansi.Strip(b.view(now))
		if ansi.StringWidth(line) >= width {
			t.Fatalf("width %d: %q", width, line)
		}
		if width >= 60 && (!strings.Contains(line, "Esc cancel") || !strings.Contains(line, "115200")) {
			t.Fatalf("width %d loses tip/config: %q", width, line)
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
	b := &StatusBar{dst: file, parser: ansi.NewParser(), started: true, active: true, width: 80, height: 24, state: "Connected"}
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
	b := NewStatusBar(output, local, nil)
	if err := b.start(session.ConnectionConfig{}); err != nil {
		t.Fatal(err)
	}
	if b.active {
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

func TestStatusBarTipsFollowModeAndRemainReadable(t *testing.T) {
	now := time.Now()
	b := &StatusBar{}
	first, _ := b.tip(now)
	if next, _ := b.tip(now.Add(time.Second)); next != first {
		t.Fatal("tip changed during normal refresh")
	}
	if next, _ := b.tip(now.Add(8 * time.Second)); next == first {
		t.Fatal("tip repeated immediately")
	}
	b.update("Connected", inputYMODEMUploadPath)
	selection, _ := b.tip(now)
	for _, key := range []string{"Tab", "<C-c>", "Enter", "Esc"} {
		if !strings.Contains(selection, key) {
			t.Fatalf("missing %s in %q", key, selection)
		}
	}
	b.update("", inputYMODEMTransfer)
	if tip, _ := b.tip(now); tip != "Esc cancel transfer" {
		t.Fatalf("transfer tip: %q", tip)
	}
	b.update("", inputNormal)
	if tip, _ := b.tip(now); tip != "" && !strings.HasPrefix(tip, "<C-p>") {
		t.Fatalf("normal tip: %q", tip)
	}
}

func TestStatusBarTipsSometimesStayQuiet(t *testing.T) {
	now := time.Now()
	b := &StatusBar{}
	last, _ := b.tip(now)
	if last == "" {
		t.Fatal("first tip should be visible")
	}
	hidden, visible := 0, 0
	for range 500 {
		now = now.Add(b.tipDuration)
		tip, compact := b.tip(now)
		if tip == "" {
			hidden++
			if compact != "" || b.tipDuration < tipHiddenMin || b.tipDuration >= tipHiddenMax {
				t.Fatalf("quiet period: compact %q, duration %v", compact, b.tipDuration)
			}
			continue
		}
		visible++
		if tip == last {
			t.Fatalf("tip repeated after a visible or quiet period: %q", tip)
		}
		last = tip
	}
	if hidden == 0 || visible == 0 {
		t.Fatalf("hidden %d, visible %d", hidden, visible)
	}
}

func TestYMODEMProgressRemainsSeparateFromStatusBar(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := &fakeEndpoint{events: make(chan session.Event, 1), cancel: cancel}
	endpoint.events <- session.YMODEMProgress{Direction: "upload", Written: 1024, Total: 2048}
	close(endpoint.events)
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	var local, footer bytes.Buffer
	bar := &StatusBar{dst: &footer, parser: ansi.NewParser(), size: func() (int, int, error) { return 80, 24, nil }}
	frontend := New(Config{Terminal: &fakeTerminal{}, Input: input, Output: &bytes.Buffer{}, Local: &local, StatusBar: bar})
	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	if line := ansi.Strip(local.String()); !strings.Contains(line, "Sending") || !strings.Contains(line, "50%") {
		t.Fatalf("missing independent progress: %q", line)
	}
	if strings.Contains(footer.String(), "50%") {
		t.Fatal("progress duplicated in status bar")
	}
}

func TestStatusBarTrafficAndWindowRate(t *testing.T) {
	now := time.Now()
	b := &StatusBar{state: "Connected", cfg: session.ConnectionConfig{PortName: "/dev/cu.usbserial", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}, tipChanged: now, samples: []trafficSample{{at: now.Add(-2 * time.Second)}}, traffic: func() (uint64, uint64) { return 1024, 3072 }}
	b.sampleTraffic(now)
	for _, width := range []int{160, 100, 80, 60, 40, 20, 160} {
		b.width = width
		line := ansi.Strip(b.view(now))
		if ansi.StringWidth(line) >= width {
			t.Fatalf("width %d overflow: %q", width, line)
		}
		if width >= 100 && !strings.Contains(line, "cu.usbserial/115200/8N1 · TX 1.0 KiB · RX 3.0 KiB · 2.0 KiB/s") {
			t.Fatalf("missing device/runtime groups: %q", line)
		}
		if width <= 60 && strings.Contains(line, "TX ") {
			t.Fatalf("runtime should be hidden on narrow terminal: %q", line)
		}
		if strings.Contains(line, "TX ") != strings.Contains(line, "2.0 KiB/s") {
			t.Fatalf("runtime must be shown as a whole: %q", line)
		}
		t.Logf("%d: %s", width, line)
	}
}

func TestTrafficRateTracksBurstAndReturnsToZero(t *testing.T) {
	start := time.Now()
	var tx, rx uint64
	b := &StatusBar{traffic: func() (uint64, uint64) { return tx, rx }}
	b.sampleTraffic(start)
	tx, rx = 1024, 1024
	b.sampleTraffic(start.Add(200 * time.Millisecond))
	if b.rate != 10240 {
		t.Fatalf("startup rate = %v, want 10240 B/s", b.rate)
	}
	for tick := 2; tick <= 10; tick++ {
		b.sampleTraffic(start.Add(time.Duration(tick) * 200 * time.Millisecond))
	}
	if b.rate != 1024 {
		t.Fatalf("two-second rate = %v, want 1024 B/s", b.rate)
	}
	b.sampleTraffic(start.Add(2100 * time.Millisecond))
	if b.rate != 512 {
		t.Fatalf("interpolated rate = %v, want 512 B/s", b.rate)
	}
	b.sampleTraffic(start.Add(2200 * time.Millisecond))
	if b.rate != 0 || b.tx != 1024 || b.rx != 1024 {
		t.Fatalf("idle: rate=%v TX=%d RX=%d", b.rate, b.tx, b.rx)
	}
	// After a long idle period the next burst still uses only the recent window.
	for tick := 12; tick <= 50; tick++ {
		b.sampleTraffic(start.Add(time.Duration(tick) * 200 * time.Millisecond))
	}
	tx += 2048
	b.sampleTraffic(start.Add(10200 * time.Millisecond))
	if b.rate != 1024 {
		t.Fatalf("new burst rate = %v, want 1024 B/s", b.rate)
	}
}

func TestStatusBarConnectionMarkerKeepsTips(t *testing.T) {
	for _, state := range []struct{ name, text string }{{"Connected", "up"}, {"Reconnecting", "retry"}, {"Disconnected", "down"}} {
		b := &StatusBar{width: 120, state: state.name, cfg: session.ConnectionConfig{PortName: "/dev/cu.usbserial", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}}
		line := b.view(time.Now())
		if !strings.HasPrefix(line, "* ["+state.text+"] cu.usbserial/") || !strings.Contains(line, "<C-p> ") {
			t.Fatalf("%s: %q", state.name, line)
		}
		b.color = true
		line = ansi.Strip(b.view(time.Now()))
		if !strings.HasPrefix(line, "* cu.usbserial/") || !strings.Contains(line, "<C-p> ") {
			t.Fatalf("colored %s: %q", state.name, line)
		}
	}
}
