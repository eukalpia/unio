package fsm

// SetApplyHookForTesting installs h to run at the start of every Apply, on
// the Raft goroutine that applies entries, or removes the hook when h is nil.
// A hook that blocks holds the state machine behind the log, which lets tests
// reproduce a replica whose Raft has committed entries that its state does
// not show yet. It is for tests only.
func SetApplyHookForTesting(f *FSM, h func(index uint64)) {
	if h == nil {
		f.applyHook.Store(nil)
		return
	}
	f.applyHook.Store(&h)
}
