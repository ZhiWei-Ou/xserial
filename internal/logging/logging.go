package logging

import (
	"fmt"
	"io"
	"strconv"
	"sync"
)

type Logger struct {
	output io.Writer
	mu     sync.Mutex
}

func New(output io.Writer) *Logger {
	return &Logger{output: output}
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

	fmt.Fprintf(l.output, "[[ xserial | %s | %s ]]", level, event)
	for i := 0; i < len(keyValues); i += 2 {
		key := fmt.Sprint(keyValues[i])
		if i+1 >= len(keyValues) {
			fmt.Fprintf(l.output, " %s=%s", key, strconv.Quote("<missing>"))
			continue
		}
		fmt.Fprintf(l.output, " %s=%s", key, formatValue(keyValues[i+1]))
	}
	fmt.Fprint(l.output, "\r\n")
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
