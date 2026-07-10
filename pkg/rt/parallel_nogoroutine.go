//go:build nogoroutine

/*
 * Copyright (c) 2026 Norman Nunley, Jr <nnunley@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package rt

// runIndexed (nogoroutine): sequential — on wasip1 there are no threads, so a
// worker pool gives no parallelism anyway; running in order is equivalent and
// lets the module build with -scheduler=none (no asyncify gowrapper).
func runIndexed(n, workers int, work func(i int)) {
	_ = workers
	for i := 0; i < n; i++ {
		work(i)
	}
}
