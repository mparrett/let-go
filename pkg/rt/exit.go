//go:build lg_profile

package rt

import "sync"

// Pre-exit hooks run just before a language-level process exit (os/exit,
// System/exit). Go's os.Exit skips deferred functions, so anything that must
// flush on the way out — e.g. an active CPU profile — registers here rather
// than via defer. Hooks are not run on signals or Go-level panics, matching the
// standard library's os.Exit semantics.
var (
	exitMu    sync.Mutex
	exitHooks []func()
	exitOnce  sync.Once
)

// AtExit registers fn to run just before a language-level os.Exit. Registration
// order is preserved; hooks run in reverse (LIFO) so later setup tears down
// first.
func AtExit(fn func()) {
	exitMu.Lock()
	exitHooks = append(exitHooks, fn)
	exitMu.Unlock()
}

// RunExitHooks runs the registered hooks in LIFO order, at most once. The
// language-level exit builtins call this immediately before os.Exit; the
// once-guard also makes it safe if a hook itself triggers another exit.
func RunExitHooks() {
	exitOnce.Do(func() {
		exitMu.Lock()
		hooks := exitHooks
		exitMu.Unlock()
		for i := len(hooks) - 1; i >= 0; i-- {
			hooks[i]()
		}
	})
}
