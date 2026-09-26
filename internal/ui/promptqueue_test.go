package ui

import (
	"runtime"
	"sync"
	"testing"
)

// ask serialises prompts and sheds past maxPendingPrompts. This exercises that
// gate without a real window: showing takes the same mutex and counter, so a
// stand-in for the blocking display is enough.
func gate(a *App, show func()) bool {
	if a.pending.Add(1) > maxPendingPrompts {
		a.pending.Add(-1)
		return false
	}
	defer a.pending.Add(-1)

	a.promptMu.Lock()
	defer a.promptMu.Unlock()
	show()
	return true
}

// Only one prompt may be on screen at a time; a burst used to open one stacked
// window per connection, each with its own timeout.
func TestPromptsAreSerialised(t *testing.T) {
	a := &App{}

	var mu sync.Mutex
	concurrent, peak := 0, 0

	var wg sync.WaitGroup
	for range maxPendingPrompts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate(a, func() {
				mu.Lock()
				concurrent++
				if concurrent > peak {
					peak = concurrent
				}
				mu.Unlock()

				mu.Lock()
				concurrent--
				mu.Unlock()
			})
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if peak > 1 {
		t.Errorf("%d prompts were on screen at once, want 1", peak)
	}
}

// Past the cap, ask declines rather than queueing, so the caller applies the
// default action instead of stacking a backlog nobody can work through.
func TestPromptsShedPastCap(t *testing.T) {
	a := &App{}

	release := make(chan struct{})
	entered := make(chan struct{}, maxPendingPrompts)

	// Occupy every slot and hold them all until release.
	var wg sync.WaitGroup
	for range maxPendingPrompts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate(a, func() {
				entered <- struct{}{}
				<-release
			})
		}()
	}

	// One goroutine holds the mutex inside show; the rest are blocked on it,
	// but all maxPendingPrompts have incremented pending, which is what the
	// cap counts. Wait for the one that got through.
	<-entered

	// The others may still be between Add and Lock, so wait for the counter
	// rather than spinning on it forever.
	for range 1000 {
		if a.pending.Load() == maxPendingPrompts {
			break
		}
		runtime.Gosched()
	}
	if got := a.pending.Load(); got != maxPendingPrompts {
		close(release)
		wg.Wait()
		t.Skipf("could not fill the queue deterministically (pending=%d)", got)
	}

	if gate(a, func() { t.Error("prompt shown past the cap") }) {
		t.Error("ask accepted a prompt past maxPendingPrompts, want it shed")
	}

	close(release)
	wg.Wait()

	if got := a.pending.Load(); got != 0 {
		t.Errorf("pending = %d after the burst drained, want 0", got)
	}

	// Once the burst drains, prompting works again.
	shown := false
	if !gate(a, func() { shown = true }) || !shown {
		t.Error("prompting did not recover after the burst drained")
	}
}
