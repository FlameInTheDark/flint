// loaders.go is the dock panels' shared loading-state machinery: a 250ms
// tick that animates the in-panel loader (elapsed seconds, spinner frame)
// while a scan or capture is in flight. Like the caret blink it never
// touches workspace state from its own goroutine — the tick is dispatched
// onto the UI thread, which updates the state and draws one frame. The
// stop channel is owned by the panel's start/finish pair.
package workspace

import (
	"strconv"
	"time"

	"github.com/FlameInTheDark/go-gpui/gpui"
)

// loaderTickInterval drives the loader's elapsed-seconds readout (four
// updates a second is smooth enough to read as "alive" without flooding
// the UI thread with redraws).
const loaderTickInterval = 250 * time.Millisecond

// startLoaderTick flips a loading panel's loader state on a 250ms tick.
// dispatch/update may be nil in headless tests (no tick — the loading
// state stays static and deterministic); w may be nil where only the
// entity update is reachable (the update's Notify still draws a frame).
func startLoaderTick(update updateFn, w *gpui.Window, dispatch func(f func()), stop chan struct{}, tick func(s *Workspace)) {
	if dispatch == nil || update == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(loaderTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				dispatch(func() {
					defer func() { _ = recover() }() // shutdown race
					update(func(s *Workspace) { tick(s) })
					if w != nil {
						w.Refresh()
					}
				})
			}
		}
	}()
}

// stopLoader closes a panel's tick channel exactly once.
func stopLoader(stop chan struct{}) {
	if stop != nil {
		select {
		case <-stop:
			// already closed
		default:
			close(stop)
		}
	}
}

// elapsedSeconds formats the time since startedAt for loader rows.
func elapsedSeconds(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "0s"
	}
	s := int(time.Since(startedAt).Seconds())
	if s < 0 {
		s = 0
	}
	return formatDurationShort(time.Duration(s) * time.Second)
}

// formatDurationShort renders "42s" / "3m05s" style durations.
func formatDurationShort(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return strconv.Itoa(s) + "s"
	}
	return strconv.Itoa(s/60) + "m" + pad2(s%60) + "s"
}

func pad2(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}
