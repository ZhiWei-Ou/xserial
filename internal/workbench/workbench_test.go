package workbench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
)

type fakeEndpoint struct {
	events chan middleware.Event
	sent   [][]byte
	err    error
	quit   bool
}

func (e *fakeEndpoint) Events() <-chan middleware.Event { return e.events }
func (e *fakeEndpoint) Send(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.sent = append(e.sent, append([]byte(nil), data...))
	return e.err
}
func (e *fakeEndpoint) StartYMODEMUpload(context.Context, string) error {
	return errors.New("not supported")
}
func (e *fakeEndpoint) StartYMODEMDownload(context.Context, string) error {
	return errors.New("not supported")
}
func (*fakeEndpoint) CancelTransfer() {}
func (e *fakeEndpoint) Quit()         { e.quit = true }

func testModel() (*model, *fakeEndpoint) {
	e := &fakeEndpoint{events: make(chan middleware.Event)}
	return newModel(context.Background(), e, Config{}), e
}

func key(m *model, code rune, text string) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: code, Text: text}))
	return cmd
}

func TestEditSendObserveAndReuseHistory(t *testing.T) {
	m, endpoint := testModel()
	m.Update(tea.PasteMsg{Content: "0xAA,0x01"})
	if len(endpoint.sent) != 0 {
		t.Fatal("paste sent before Enter")
	}
	cmd := key(m, tea.KeyEnter, "")
	if cmd == nil || !m.sending || len(m.entries) != 0 {
		t.Fatal("send was not pending until confirmation")
	}
	if duplicate := key(m, tea.KeyEnter, ""); duplicate != nil {
		t.Fatal("duplicate pending send")
	}
	m.Update(cmd())
	if !bytes.Equal(endpoint.sent[0], []byte{0xAA, 1}) || m.txBytes != 2 || m.entries[0].Direction != "TX" {
		t.Fatalf("sent = % X, entries = %v", endpoint.sent, m.entries)
	}
	m.handleEvent(middleware.Received{Data: []byte{0xBB, 2, 0x1B}, At: time.Now()})
	if m.rxBytes != 3 || len(m.entries) != 2 {
		t.Fatal("response missing")
	}
	m.setInput("12")
	key(m, tea.KeyUp, "")
	if string(m.input) != "AA 01" {
		t.Fatal("history did not load canonical bytes")
	}
	key(m, tea.KeyDown, "")
	if string(m.input) != "12" {
		t.Fatal("history lost draft")
	}
	key(m, tea.KeyHome, "")
	key(m, '0', "00 ")
	if string(m.input) != "00 12" {
		t.Fatalf("edited input = %q", m.input)
	}
}

func TestInvalidDisconnectedAndFailedSendNeverShowSuccessfulTX(t *testing.T) {
	m, endpoint := testModel()
	for _, input := range []string{"", "AA G0", "A"} {
		m.setInput(input)
		if key(m, tea.KeyEnter, "") != nil {
			t.Fatalf("invalid %q was sent", input)
		}
	}
	m.setInput("AA 01")
	m.handleEvent(middleware.Disconnected{Err: errors.New("unplugged")})
	if key(m, tea.KeyEnter, "") != nil || len(endpoint.sent) != 0 {
		t.Fatal("disconnected send was queued")
	}
	m.handleEvent(middleware.Reconnected{})
	endpoint.err = errors.New("short write")
	cmd := key(m, tea.KeyEnter, "")
	m.Update(cmd())
	if len(m.entries) != 0 || m.txBytes != 0 || string(m.input) != "AA 01" || m.sending || !strings.Contains(m.status, "short write") {
		t.Fatalf("failed send state = %+v", m)
	}
}

func TestScrollStaysOnExistingTrafficWhileReceivingAndHistoryIsBounded(t *testing.T) {
	m, _ := testModel()
	m.height = 12
	for i := range 20 {
		m.appendTraffic(trafficEntry{Direction: "RX", At: time.Now(), Data: []byte{byte(i)}})
	}
	key(m, tea.KeyPgUp, "")
	before := ansi.Strip(m.View().Content)
	m.handleEvent(middleware.Received{Data: []byte{0xFF}, At: time.Now()})
	after := ansi.Strip(m.View().Content)
	beforeLines, afterLines := strings.Split(before, "\n"), strings.Split(after, "\n")
	if strings.Join(beforeLines[2:7], "\n") != strings.Join(afterLines[2:7], "\n") {
		t.Fatal("new data moved the paused viewport")
	}
	key(m, tea.KeyEnd, "")
	if m.scroll != 0 {
		t.Fatal("End did not resume follow")
	}
	data := bytes.Repeat([]byte{0x42}, 4096)
	for range 300 {
		m.appendTraffic(trafficEntry{Direction: "RX", At: time.Now(), Data: data})
	}
	if m.trafficBytes > maxTrafficBytes || len(m.entries) > maxEntries {
		t.Fatal("unbounded traffic")
	}
	for i := range 200 {
		m.remember(fmt.Sprintf("%04X", i))
	}
	if len(m.history) != maxHistory {
		t.Fatal("unbounded input history")
	}
}

func TestFavoritesCanBeNamedSavedLoadedAndEdited(t *testing.T) {
	m, _ := testModel()
	m.cfg.FavoritesPath = filepath.Join(t.TempDir(), "commands.json")
	m.setInput("AA 01")
	m.action("save")
	key(m, 'q', "Query")
	cmd := key(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("no save command")
	}
	m.Update(cmd())
	m.setInput("FF")
	m.action("load")
	key(m, tea.KeyEnter, "")
	if string(m.input) != "AA 01" || m.modal != "" {
		t.Fatal("favorite did not load for editing")
	}
	got, err := LoadFavorites(m.cfg.FavoritesPath)
	if err != nil || len(got) != 1 || got[0].Name != "Query" {
		t.Fatalf("favorites = %v, %v", got, err)
	}
}

func TestWorkbenchNeverExecutesDeviceANSIAndFitsSmallTerminals(t *testing.T) {
	for _, size := range [][2]int{{100, 28}, {50, 18}, {25, 10}, {10, 5}} {
		m, _ := testModel()
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.handleEvent(middleware.Received{Data: []byte("\x1b[2J\r\n\xff"), At: time.Now()})
		view := ansi.Strip(m.View().Content)
		if strings.Contains(view, "\x1b") {
			t.Fatal("device escape sequence leaked")
		}
		lines := strings.Split(view, "\n")
		if len(lines) > size[1] {
			t.Fatalf("height %d exceeded by %d", size[1], len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("width overflow: %q", line)
			}
		}
	}
}

func TestCanceledCommandDoesNotWrite(t *testing.T) {
	m, endpoint := testModel()
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.setInput("AA")
	cmd := key(m, tea.KeyEnter, "")
	cancel()
	m.Update(cmd())
	if len(endpoint.sent) != 0 || m.txBytes != 0 {
		t.Fatal("write continued after cancellation")
	}
}
