package chatserver

import "github.com/docker/docker-agent/pkg/concurrent"

// conversationLockSet ensures only one in-flight request at a time per
// conversation id. Concurrent requests sharing an id would otherwise share
// the same `*session.Session` (the cache hands out the same pointer to every
// caller for that id), and two concurrent runtime.RunStream calls on one
// session interleave message appends and produce garbled transcripts.
//
// We reject the second request with 409 Conflict instead of serialising it,
// for two reasons: it surfaces the misuse to the client immediately, and it
// keeps the handler's resource cost bounded (no queue, no waiting goroutines).
type conversationLockSet struct {
	active concurrent.Map[string, struct{}]
}

func newConversationLockSet() *conversationLockSet {
	return &conversationLockSet{}
}

// tryAcquire returns true when id was not already in flight. The caller
// must call release when the request finishes. Empty id is a no-op (and
// returns true) so callers without a conversation id don't need a guard.
func (l *conversationLockSet) tryAcquire(id string) bool {
	if l == nil || id == "" {
		return true
	}
	_, loaded := l.active.LoadOrStore(id, struct{}{})
	return !loaded
}

// release marks id as no longer in flight. Safe to call when id is the
// empty string or l is nil.
func (l *conversationLockSet) release(id string) {
	if l == nil || id == "" {
		return
	}
	l.active.Delete(id)
}
