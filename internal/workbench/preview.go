package workbench

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"io"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
)

type previewFrontend struct{ cfg Config }

// NewPreview runs one simulated query and renders the same view as the interactive
// workbench, without entering raw mode. It supports demos in pipes and documentation.
func NewPreview(cfg Config) middleware.Frontend { return &previewFrontend{cfg: cfg} }

func (f *previewFrontend) Run(ctx context.Context, endpoint middleware.Endpoint) error {
	m := newModel(ctx, endpoint, f.cfg)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	request, _ := hexdata.Parse("01 03 00 00 00 02 C4 0B")
	if err := endpoint.Send(ctx, request); err != nil {
		return err
	}
	m.Update(sentMsg{data: request, input: hexdata.Format(request), at: time.Now()})
	for m.rxBytes < 9 {
		select {
		case event, ok := <-m.events:
			if !ok {
				return io.ErrUnexpectedEOF
			}
			m.handleEvent(event)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.status = "Demo query complete · Tab inspects fields · Ctrl-K appends checksums"
	_, err := io.WriteString(f.cfg.Output, m.View().Content+"\n")
	return err
}
