package workbench

import (
	"sync"

	tea "charm.land/bubbletea/v2"
)

// Bubble Tea may finish before a dispatched command returns. Track commands that
// access session resources or files, and reject ones dispatched after shutdown.
type commandTasks struct {
	mu       sync.Mutex
	stopping bool
	wg       sync.WaitGroup
}

func (t *commandTasks) wrap(command tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		t.mu.Lock()
		if t.stopping {
			t.mu.Unlock()
			return nil
		}
		t.wg.Add(1)
		t.mu.Unlock()
		defer t.wg.Done()
		return command()
	}
}

func (t *commandTasks) stop() {
	t.mu.Lock()
	t.stopping = true
	t.mu.Unlock()
	t.wg.Wait()
}
