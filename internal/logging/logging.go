package logging

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type Level uint8

const (
	InfoLevel Level = iota
	WarnLevel
	ErrorLevel
)

func (l Level) String() string {
	switch l {
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

type Entry struct {
	Time    time.Time
	Level   Level
	Message string
	Fields  []any
}

type TextFormatter struct {
	Color bool
}

// EventFormatter keeps machine-auditable business events separate from device
// logs. CRLF also makes it suitable for the raw terminal's local output.
type EventFormatter struct{}

func (EventFormatter) Format(entry Entry) []byte {
	var line bytes.Buffer
	fmt.Fprintf(&line, "[ %s | %s ] time=%s", entry.Level, entry.Message, strconv.Quote(entry.Time.UTC().Format(time.RFC3339Nano)))
	for i := 0; i+1 < len(entry.Fields); i += 2 {
		fmt.Fprintf(&line, " %s=%s", entry.Fields[i], formatValue(entry.Fields[i+1]))
	}
	line.WriteString("\r\n")
	return line.Bytes()
}

func (f TextFormatter) Format(entry Entry) []byte {
	var line bytes.Buffer
	fmt.Fprintf(&line, "%s [", entry.Time.Format("15:04:05.000"))
	level := entry.Level.String()
	if color := levelColor(entry.Level); f.Color && color != "" {
		fmt.Fprintf(&line, "\x1b[%sm%s\x1b[0m", color, level)
	} else {
		fmt.Fprint(&line, level)
	}
	fmt.Fprintf(&line, "] %s", entry.Message)
	for i := 0; i < len(entry.Fields); i += 2 {
		key := fmt.Sprint(entry.Fields[i])
		if i+1 >= len(entry.Fields) {
			fmt.Fprintf(&line, " %s=%s", key, strconv.Quote("<missing>"))
			continue
		}
		fmt.Fprintf(&line, " %s=%s", key, formatValue(entry.Fields[i+1]))
	}
	fmt.Fprint(&line, "\r\n")
	return line.Bytes()
}

// Logger writes application logs and unformatted local output. A nil *Logger
// discards both without returning an error.
type Logger struct {
	output    io.Writer
	formatter TextFormatter
	mu        sync.Mutex
}

func New(output io.Writer) *Logger {
	return &Logger{
		output:    output,
		formatter: TextFormatter{Color: supportsColor(output)},
	}
}

func (l *Logger) Warn(message string, fields ...any) {
	l.log(WarnLevel, message, fields...)
}

func (l *Logger) Error(message string, fields ...any) {
	l.log(ErrorLevel, message, fields...)
}

func (l *Logger) log(level Level, message string, fields ...any) {
	if l == nil || l.output == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	entry := Entry{Time: time.Now(), Level: level, Message: message, Fields: fields}
	_, _ = l.output.Write(l.formatter.Format(entry))
}

// Raw writes data exactly as provided, without log formatting.
func (l *Logger) Raw(data []byte) (int, error) {
	if l == nil || l.output == nil {
		return len(data), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.output.Write(data)
}

// Write makes Logger an io.Writer backed by Raw.
func (l *Logger) Write(data []byte) (int, error) {
	return l.Raw(data)
}

func supportsColor(output io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	file, ok := output.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

func levelColor(level Level) string {
	switch level {
	case InfoLevel:
		return "32"
	case WarnLevel:
		return "33"
	case ErrorLevel:
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
