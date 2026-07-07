//go:build (js && wasm) || wasip1

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

// Pods are not supported in WASM — subprocess execution is unavailable.

func init() { RegisterInstaller(installPodsNS) }

func installPodsNS() {}

func ShutdownAllPods() {}
