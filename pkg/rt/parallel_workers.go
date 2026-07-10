//go:build !nogoroutine

/*
 * Copyright (c) 2026 Norman Nunley, Jr <nnunley@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"sync"
	"sync/atomic"
)

// runIndexed fans work(i) for i in [0,n) across a pool of workers. The
// nogoroutine build variant runs the indexes sequentially instead so the
// module links with no goroutine machinery (TinyGo -scheduler=none).
func runIndexed(n, workers int, work func(i int)) {
	var next int64 = -1
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= n {
					return
				}
				work(i)
			}
		}()
	}
	wg.Wait()
}
