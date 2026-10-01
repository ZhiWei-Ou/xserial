package workbench

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestShutdownWaitsForCommandsAndRejectsLateDispatch(t *testing.T) {
	var tasks commandTasks
	started := make(chan struct{})
	finish := make(chan struct{})
	returned := make(chan struct{})
	cmd := tasks.wrap(func() tea.Msg { close(started); <-finish; return nil })
	go func() { cmd(); close(returned) }()
	<-started
	stopped := make(chan struct{})
	go func() { tasks.stop(); close(stopped) }()
	close(finish)
	<-stopped
	<-returned
	late := tasks.wrap(func() tea.Msg { t.Error("late command touched a closed resource"); return nil })
	if late() != nil {
		t.Fatal("late command was dispatched")
	}
}
