package main

import (
	goruntime "runtime"
	"sync"
	"sync/atomic"
)

// parallelMinItems is the fewest items parallelRange spreads over workers;
// fewer run in the calling goroutine, where starting workers would cost more
// than it saves.
const parallelMinItems = 128

// parallelMaxWorkers caps the workers of one parallelRange.
const parallelMaxWorkers = 8

// parallelRange calls fn(i) for every i in [0, n), spread over up to
// GOMAXPROCS workers that take indices in blocks, and returns when every
// call has returned. fn must touch only what index i owns: a plan
// computation builds and encodes each fleet node independently of the
// others, which is most of its time at 1000 rows and more.
func parallelRange(n int, fn func(i int)) {
	workers := min(goruntime.GOMAXPROCS(0), parallelMaxWorkers)
	if n < parallelMinItems || workers < 2 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	const block = 32
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				start := int(next.Add(block)) - block
				if start >= n {
					return
				}
				for i := start; i < min(start+block, n); i++ {
					fn(i)
				}
			}
		}()
	}
	wg.Wait()
}
