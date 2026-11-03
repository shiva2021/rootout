package tui

import tea "charm.land/bubbletea/v2"

// embeddedPicker is a picker another screen can host over itself: the host
// forwards messages, reads done() after each one and closes it with
// cancel(). ModelPicker (Ollama) and HostedPicker (Claude, OpenAI) both
// implement it.
type embeddedPicker interface {
	tea.Model
	// done reports whether the picker has finished and what it chose.
	done() (model string, chosen, finished bool)
	// cancel stops any background work (a download, a fetch).
	cancel()
	// setEmbedded makes finish-instead-of-quit the picker's end state.
	setEmbedded(on bool)
}
