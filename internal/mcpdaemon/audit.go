package mcpdaemon

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/logging"
)

type auditEntry struct {
	Sequence uint64
	Line     []byte
}
type AuditInput struct {
	After uint64
	Now   bool
}
type AuditResult struct {
	Next  uint64
	Lines [][]byte
}

// A bounded audit stream lets stdio bridges observe an existing daemon without
// files or blocking the daemon behind a slow subscriber. Gaps are explicit.
type auditLog struct {
	mu       sync.Mutex
	output   io.Writer
	entries  []auditEntry
	sequence uint64
	changed  chan struct{}
}

func newAudit(output io.Writer) *auditLog {
	return &auditLog{output: output, changed: make(chan struct{})}
}

func (a *auditLog) event(event string, fields ...any) {
	line := logging.EventFormatter{}.Format(logging.Entry{Time: time.Now(), Level: logging.InfoLevel, Message: event, Fields: fields})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.output != nil {
		_, _ = a.output.Write(line)
	}
	a.sequence++
	if len(a.entries) == 512 {
		copy(a.entries, a.entries[1:])
		a.entries = a.entries[:511]
	}
	a.entries = append(a.entries, auditEntry{a.sequence, line})
	close(a.changed)
	a.changed = make(chan struct{})
}

func (a *auditLog) read(ctx context.Context, in AuditInput) (AuditResult, error) {
	for {
		a.mu.Lock()
		result := AuditResult{Next: a.sequence}
		if in.Now {
			a.mu.Unlock()
			return result, nil
		}
		if len(a.entries) > 0 && in.After+1 < a.entries[0].Sequence {
			result.Lines = append(result.Lines, logging.EventFormatter{}.Format(logging.Entry{Time: time.Now(), Level: logging.WarnLevel, Message: "mcp.audit_gap", Fields: []any{"dropped_events", a.entries[0].Sequence - in.After - 1}}))
		}
		for _, entry := range a.entries {
			if entry.Sequence > in.After {
				result.Lines = append(result.Lines, entry.Line)
			}
		}
		changed := a.changed
		a.mu.Unlock()
		if len(result.Lines) > 0 {
			return result, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
}

func (c *Client) FollowAudit(ctx context.Context, after uint64, output io.Writer) error {
	for {
		var result AuditResult
		if err := c.Call(ctx, "audit", AuditInput{After: after}, &result); err != nil {
			return err
		}
		for _, line := range result.Lines {
			if _, err := output.Write(line); err != nil {
				return err
			}
		}
		after = result.Next
	}
}
