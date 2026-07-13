package logging

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

type Logger struct {
	output io.Writer
	color  bool
	mu     sync.Mutex
}

func New(output io.Writer) *Logger {
	return &Logger{output: output, color: supportsColor(output)}
}

func (l *Logger) Info(event string, keyValues ...any) {
	l.write("INFO", event, keyValues...)
}

func (l *Logger) Warn(event string, keyValues ...any) {
	l.write("WARN", event, keyValues...)
}

func (l *Logger) Error(event string, keyValues ...any) {
	l.write("ERROR", event, keyValues...)
}

func (l *Logger) write(level string, event string, keyValues ...any) {
	if l == nil || l.output == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var line bytes.Buffer
	fmt.Fprint(&line, "[ ")
	if color := levelColor(level); l.color && color != "" {
		fmt.Fprintf(&line, "\x1b[%sm%s\x1b[0m", color, level)
	} else {
		fmt.Fprint(&line, level)
	}
	fmt.Fprintf(&line, " | %s ]", event)
	for i := 0; i < len(keyValues); i += 2 {
		key := fmt.Sprint(keyValues[i])
		if i+1 >= len(keyValues) {
			fmt.Fprintf(&line, " %s=%s", key, strconv.Quote("<missing>"))
			continue
		}
		fmt.Fprintf(&line, " %s=%s", key, formatValue(keyValues[i+1]))
	}
	fmt.Fprint(&line, "\r\n")
	_, _ = l.output.Write(line.Bytes())
}

func supportsColor(output io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	file, ok := output.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

func levelColor(level string) string {
	switch level {
	case "INFO":
		return "32"
	case "WARN":
		return "33"
	case "ERROR":
		return "31"
	default:
		return ""
	}
}

func formatValue(value any) string {
	switch value := value.(type) {
	case string:
		return strconv.Quote(value)
	case []byte:
		return strconv.Quote(string(value))
	case error:
		return strconv.Quote(value.Error())
	default:
		return fmt.Sprint(value)
	}
}
