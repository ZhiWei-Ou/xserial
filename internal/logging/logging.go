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
	TraceLevel Level = iota
	DebugLevel
	InfoLevel
	WarnLevel
	ErrorLevel
	PanicLevel
)

func (l Level) String() string {
	switch l {
	case TraceLevel:
		return "TRACE"
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	case PanicLevel:
		return "PANIC"
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

type Formatter interface {
	Format(Entry) []byte
}

type TextFormatter struct {
	Color bool
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

type Option func(*Logger)

func WithLevel(level Level) Option {
	return func(logger *Logger) {
		logger.level = level
	}
}

func WithFormatter(formatter Formatter) Option {
	return func(logger *Logger) {
		if formatter != nil {
			logger.formatter = formatter
		}
	}
}

// Logger writes application logs and unformatted local output. A nil *Logger
// discards both without returning an error.
type Logger struct {
	output    io.Writer
	level     Level
	formatter Formatter
	mu        sync.Mutex
}

func New(output io.Writer, options ...Option) *Logger {
	logger := &Logger{
		output:    output,
		level:     InfoLevel,
		formatter: TextFormatter{Color: supportsColor(output)},
	}
	for _, option := range options {
		option(logger)
	}
	return logger
}

func (l *Logger) Trace(message string, fields ...any) {
	l.log(TraceLevel, message, fields...)
}

func (l *Logger) Debug(message string, fields ...any) {
	l.log(DebugLevel, message, fields...)
}

func (l *Logger) Info(message string, fields ...any) {
	l.log(InfoLevel, message, fields...)
}

func (l *Logger) Warn(message string, fields ...any) {
	l.log(WarnLevel, message, fields...)
}

func (l *Logger) Error(message string, fields ...any) {
	l.log(ErrorLevel, message, fields...)
}

func (l *Logger) Panic(message string, fields ...any) {
	l.log(PanicLevel, message, fields...)
	panic(message)
}

func (l *Logger) log(level Level, message string, fields ...any) {
	if l == nil || l.output == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if level < l.level {
		return
	}
	entry := Entry{Time: time.Now(), Level: level, Message: message, Fields: fields}
	_, _ = l.output.Write(l.formatter.Format(entry))
}

// Raw writes data exactly as provided. It bypasses level filtering and the
// configured Formatter.
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
	case TraceLevel:
		return "90"
	case DebugLevel:
		return "36"
	case InfoLevel:
		return "32"
	case WarnLevel:
		return "33"
	case ErrorLevel:
		return "31"
	case PanicLevel:
		return "35"
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
