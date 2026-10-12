package rawui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const uploadPrompt = "YMODEM upload file: "

// pathPrompt owns only local file selection; no input here reaches the device.
type pathPrompt struct {
	line    []rune
	cursor  int
	pending []byte
	paste   bool
	width   int
}

func (p *pathPrompt) render(w io.Writer) {
	// Keep long paths on one physical line, including on narrow terminals.
	width := p.width
	if width < 1 {
		width = 80
	}
	label := uploadPrompt
	if ansi.StringWidth(label)+10 >= width {
		label = "File: "
	}
	room := max(1, width-ansi.StringWidth(label)-1)
	start := 0
	for start < p.cursor && ansi.StringWidth(string(p.line[start:p.cursor])) >= room {
		start++
	}
	visible := ansi.Truncate(string(p.line[start:]), room, "")
	fmt.Fprintf(w, "\r\x1b[2K%s%s\r", localColor(label, localInfoColor), visible)
	if col := ansi.StringWidth(label) + ansi.StringWidth(string(p.line[start:p.cursor])); col > 0 {
		fmt.Fprintf(w, "\x1b[%dC", col)
	}
}

// feed returns a completed/canceled selection. A lone Esc is resolved by the
// frontend's short timer so arrow-key and paste sequences remain local.
func (p *pathPrompt) feed(b byte, w io.Writer) (submit, cancel bool) {
	if len(p.pending) > 0 && p.pending[0] == 0x1b {
		p.pending = append(p.pending, b)
		if len(p.pending) == 2 {
			if b == '[' || b == 'O' {
				return
			}
			p.pending = nil
			return false, true
		}
		if b < 0x40 || b > 0x7e {
			return
		}
		seq := string(p.pending)
		p.pending = nil
		if seq == "\x1b[201~" {
			p.paste = false
			return
		}
		if p.paste {
			return
		}
		switch seq {
		case "\x1b[200~":
			p.paste = true
		case "\x1b[D", "\x1bOD":
			p.cursor = max(0, p.cursor-1)
		case "\x1b[C", "\x1bOC":
			p.cursor = min(len(p.line), p.cursor+1)
		case "\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~":
			p.cursor = 0
		case "\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~":
			p.cursor = len(p.line)
		case "\x1b[3~":
			if p.cursor < len(p.line) {
				p.line = append(p.line[:p.cursor], p.line[p.cursor+1:]...)
			}
		}
		p.render(w)
		return
	}
	if b == 0x1b {
		p.pending = []byte{b}
		return
	}
	if p.paste && (b < 0x20 || b == 0x7f) {
		return
	}
	switch b {
	case '\r', '\n':
		return true, false
	case 0x03:
		p.line = nil
		p.cursor = 0
		p.pending = nil // Ctrl-C clears the entire path without leaving the prompt.
	case '\t':
		prefix := string(p.line[:p.cursor])
		completed, candidates, err := completePath(prefix)
		if err != nil {
			fmt.Fprintf(w, "\r\n%v\r\n", err)
		} else {
			p.line = append([]rune(completed), p.line[p.cursor:]...)
			p.cursor = len([]rune(completed))
			if len(candidates) > 1 && completed == prefix {
				fmt.Fprint(w, "\r\n")
				for _, candidate := range candidates {
					fmt.Fprintf(w, "%s\r\n", candidate)
				}
			}
		}
	case 0x7f, '\b':
		if p.cursor > 0 {
			p.line = append(p.line[:p.cursor-1], p.line[p.cursor:]...)
			p.cursor--
		}
	case 0x01:
		p.cursor = 0 // Ctrl-A
	case 0x05:
		p.cursor = len(p.line) // Ctrl-E
	case 0x02:
		p.cursor = max(0, p.cursor-1) // Ctrl-B
	case 0x06:
		p.cursor = min(len(p.line), p.cursor+1) // Ctrl-F
	case 0x15:
		p.line = p.line[p.cursor:]
		p.cursor = 0 // Ctrl-U
	case 0x0b:
		p.line = p.line[:p.cursor] // Ctrl-K
	default:
		if b < 0x20 {
			return
		}
		p.pending = append(p.pending, b)
		if !utf8.FullRune(p.pending) {
			return
		}
		r, _ := utf8.DecodeRune(p.pending)
		p.pending = nil
		if unicode.IsPrint(r) {
			p.line = append(p.line[:p.cursor], append([]rune{r}, p.line[p.cursor:]...)...)
			p.cursor++
		}
	}
	p.render(w)
	return
}

func inputPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		value = value[1 : len(value)-1]
	}
	if value == "~" || strings.HasPrefix(value, "~/") || (os.PathSeparator == '\\' && strings.HasPrefix(value, "~\\")) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(home, strings.TrimLeft(value[1:], `/\`))
	}
	return value, nil
}

func completePath(value string) (string, []string, error) {
	// Preserve the user's relative or ~/ spelling in the editor.
	quote := ""
	if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
		quote, value = value[:1], value[1:]
		value = strings.TrimSuffix(value, quote)
	}
	if value == "~" {
		value += string(os.PathSeparator)
	}
	dir, base := filepath.Split(value)
	lookup, err := inputPath(dir)
	if err != nil {
		return quote + value, nil, err
	}
	if lookup == "" {
		lookup = "."
	}
	entries, err := os.ReadDir(lookup)
	if err != nil {
		return quote + value, nil, err
	}
	var matches []string
	foldedBase := strings.ToLower(base)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), foldedBase) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		info, err := os.Stat(filepath.Join(lookup, name))
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			continue
		}
		if info.IsDir() {
			name += string(os.PathSeparator)
		}
		// Control characters in filenames cannot be represented by this line editor.
		if strings.IndexFunc(name, unicode.IsControl) >= 0 {
			continue
		}
		matches = append(matches, name)
	}
	if len(matches) == 0 {
		return quote + value, nil, nil
	}
	common := []rune(matches[0])
	for _, match := range matches[1:] {
		runes := []rune(match)
		n := 0
		for n < len(common) && n < len(runes) && common[n] == runes[n] {
			n++
		}
		common = common[:n]
	}
	// Keep ambiguous casing editable instead of choosing a candidate or erasing input.
	if len(matches) > 1 && len(common) < len([]rune(base)) {
		return quote + value, matches, nil
	}
	result := quote + dir + string(common)
	if len(matches) == 1 && quote != "" && !strings.HasSuffix(matches[0], string(os.PathSeparator)) {
		result += quote
	}
	return result, matches, nil
}
