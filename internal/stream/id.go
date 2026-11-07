package stream

import (
	"strconv"
	"sync/atomic"
)

// streamIDCounter backs process-unique stream correlation IDs for callers
// that do not supply one.
var streamIDCounter uint64

// newStreamID generates a process-unique correlation ID. Callers wanting
// human-meaningful IDs (project + target) pass Options.StreamID instead.
func newStreamID() string {
	n := atomic.AddUint64(&streamIDCounter, 1)
	return "stream-" + strconv.FormatUint(n, 10)
}
