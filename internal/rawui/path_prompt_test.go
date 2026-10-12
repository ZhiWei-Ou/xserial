package rawui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
)

func TestUploadPathEditingAndCompletion(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"firmware one.bin", "firmware two.bin", "固件.bin", ".hidden", "Release.bin", "Äudio.bin", "MixedOne.bin", "mixedTwo.bin"} {
		if err := os.WriteFile(name, []byte("firmware"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir("build", 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, input, want string }{
		{"common prefix", "fi\t", "firmware "},
		{"uppercase common prefix", "FI\t", "firmware "},
		{"preserve filename case", "rel\t", "Release.bin"},
		{"Unicode case", "äu\t", "Äudio.bin"},
		{"ambiguous casing keeps input", "MIX\t\t", "MIX"},
		{"space in filename", "firmware o\t", "firmware one.bin"},
		{"quoted filename", "\"firmware o\t", "\"firmware one.bin\""},
		{"directory", "bu\t", "build" + string(os.PathSeparator)},
		{"uppercase directory", "BU\t", "build" + string(os.PathSeparator)},
		{"UTF8 backspace", "固错\x7f件.b\t", "固件.bin"},
		{"insert at cursor", "fime\x1b[D\x1b[Dr", "firme"},
		{"delete home end", "XfirmwareX\x1b[H\x1b[3~\x1b[F\x7f", "firmware"},
		{"clear input", "wrong\x15fi\t", "firmware "},
		{"unsupported key stays local", "fi\x1b[A\t", "firmware "},
		{"bracketed paste", "\x1b[200~firmware one.bin\r\n\x1b[201~", "firmware one.bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p pathPrompt
			var out bytes.Buffer
			for _, b := range []byte(tc.input) {
				if submit, cancel := p.feed(b, &out); submit || cancel {
					t.Fatalf("unexpected submit=%v cancel=%v", submit, cancel)
				}
			}
			if got := string(p.line); got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
	var p pathPrompt
	var out bytes.Buffer
	for _, b := range []byte("FI\t\t") {
		p.feed(b, &out)
	}
	if !strings.Contains(out.String(), "firmware one.bin\r\nfirmware two.bin\r\n") {
		t.Fatalf("missing candidates: %q", out.String())
	}
}

func TestUploadPathCaseCollision(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"Firmware.bin", "firmware.bin"} {
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Skip("filesystem does not support names differing only in case")
	}
	var p pathPrompt
	var out bytes.Buffer
	for _, b := range []byte("fir\t") {
		p.feed(b, &out)
	}
	if string(p.line) != "fir" || !strings.Contains(out.String(), "Firmware.bin\r\nfirmware.bin\r\n") {
		t.Fatalf("ambiguous completion: path=%q output=%q", string(p.line), out.String())
	}
}

func TestUploadPathHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := inputPath("\"~/firmware one.bin\"")
	if err != nil || got != filepath.Join(home, "firmware one.bin") {
		t.Fatalf("path=%q err=%v", got, err)
	}
}

func TestUploadFileSelectionRetriesAndKeepsKeysLocal(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("固件 one.bin", []byte("firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel}
	var local bytes.Buffer
	frontend := New(Config{
		Terminal: &fakeTerminal{},
		Input:    strings.NewReader("\x10\x15missing\r\x15固错\x7f件 O\t\x1b[D\x1b[C\r"),
		Output:   &bytes.Buffer{}, Local: &local,
	})
	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint.ymodemUpload != "固件 one.bin" {
		t.Fatalf("upload=%q", endpoint.ymodemUpload)
	}
	if endpoint.sent.Len() != 0 {
		t.Fatalf("file selection leaked to serial: %q", endpoint.sent.String())
	}
	if !strings.Contains(local.String(), "YMODEM upload failed:") || !strings.Contains(local.String(), "Waiting for receiver") {
		t.Fatalf("output=%q", local.String())
	}
	if !strings.Contains(local.String(), "\x1b[?2004h") || !strings.Contains(local.String(), "\x1b[?2004l") {
		t.Fatal("bracketed paste not restored")
	}
}

func TestUploadPathReusedWithinSession(t *testing.T) {
	t.Chdir(t.TempDir())
	const name = "固件 one.bin"
	if err := os.WriteFile(name, []byte("firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, input string }{
		{"repeat with Enter", "\x10\x15\r"},
		{"canceled edit preserves previous path", "\x10\x15\x15wrong.bin\x1by\x10\x15\r"},
		{"edit prefilled path", "\x10\x15\x15./固件 one.bin\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel, autoFinish: true}
			input, inputWriter, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer inputWriter.Close()
			local := &signalWriter{match: []byte("✓ Sent"), done: make(chan struct{})}
			frontend := New(Config{Terminal: &fakeTerminal{}, Input: input, Output: &bytes.Buffer{}, Local: local})
			result := make(chan error, 1)
			go func() { result <- frontend.Run(ctx, endpoint) }()
			if _, err := inputWriter.WriteString("\x10\x15" + name + "\r"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-local.done:
			case <-ctx.Done():
				t.Fatal("first upload did not finish")
			}
			if _, err := inputWriter.WriteString(tc.input); err != nil {
				t.Fatal(err)
			}
			inputWriter.Close()
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(local.String(), "Waiting for receiver"); got != 2 {
				t.Fatalf("started %d uploads, want 2: %q", got, local.String())
			}
			if !strings.Contains(local.String(), uploadPrompt+name) {
				t.Fatalf("missing prefilled path: %q", local.String())
			}
		})
	}
}
