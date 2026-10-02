package workbench

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"io"
)

const (
	maxEntries      = 2000
	maxTrafficBytes = 1 << 20
	maxHistory      = 100
	maxInputChars   = 16384
)

type Config struct {
	Input         io.Reader
	Output        io.Writer
	Connection    middleware.ConnectionConfig
	FavoritesPath string
	Favorites     []Favorite
	Demo          bool
	Framing       hexdata.FrameConfig
	Recorder      *capture.Writer
}

type Frontend struct{ cfg Config }

func New(cfg Config) *Frontend { return &Frontend{cfg: cfg} }

func (f *Frontend) Run(ctx context.Context, endpoint middleware.Endpoint) error {
	if f.cfg.Input == nil || f.cfg.Output == nil {
		return errors.New("workbench input and output are required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m := newModel(runCtx, endpoint, f.cfg)
	defer func() {
		cancel()
		m.tasks.stop()
	}()
	program := tea.NewProgram(m, tea.WithContext(runCtx), tea.WithInput(f.cfg.Input), tea.WithOutput(f.cfg.Output))
	_, err := program.Run()
	if ctx.Err() != nil || errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}
