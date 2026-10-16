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
// GOMAXPROCS workers (the calling goroutine one of them) that take indices
// in blocks, and returns when every call has returned. fn must touch only
// what index i owns: a plan computation builds and encodes each fleet node
// independently of the others, which is most of its time at 1000 rows and
// more.
//
// A panic in fn reaches the caller as it would from a plain loop: the worker
// that panicked recovers, the others stop taking indices, and once all have
// returned parallelRange panics again on the calling goroutine with the
// first value, so the runtime's per-invocation recover turns it into one
// failed call. Left on a worker goroutine it would end the worker process.
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
	var first atomic.Pointer[parallelPanic]
	work := func() {
		defer func() {
			if v := recover(); v != nil {
				first.CompareAndSwap(nil, &parallelPanic{value: v})
				next.Store(int64(n))
			}
		}()
		for {
			start := int(next.Add(block)) - block
			if start >= n {
				return
			}
			for i := start; i < min(start+block, n); i++ {
				fn(i)
			}
		}
	}
	var wg sync.WaitGroup
	for w := 1; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			work()
		}()
	}
	work()
	wg.Wait()
	if p := first.Load(); p != nil {
		panic(p.value)
	}
}

// parallelPanic carries a recovered panic value from a worker to the caller.
type parallelPanic struct{ value any }
