//go:build tinygo && js

/*
 * Copyright (c) 2026 Matt Parrett
 * SPDX-License-Identifier: MIT
 *
 * TinyGo hand-written twin of the lginterop-generated interop_xxh3.go
 * (which is //go:build !tinygo). TinyGo's reflect can't box or call typed Go
 * funcs (reflect.Value.Call unimplemented), so the generated file's MustBox
 * path is unusable here. Scoped to js/wasm: zeebo/xxh3 has no
 * purego mode on arm64/amd64 (assembly-only), which TinyGo can't link, so a
 * NATIVE tinygo build has no xxh3 NS yet (needs a pure-Go xxh3). But xxh3's pure-Go implementation IS available under
 * GOOS=js (no NEON assembly), so we call it directly through Wrap adapters —
 * no reflection. Covers the u64 scalar API (Hash/HashSeed and their String
 * forms) that consumers actually use (xsofy.hash → xxh3/HashSeed). The 128-bit
 * and streaming Hasher variants are intentionally omitted: nothing uses them,
 * and they return opaque values that would need reflect-free Boxed wrapping.
 */

package rt

import (
	"github.com/nooga/let-go/pkg/vm"
	xxh3 "github.com/zeebo/xxh3"
)

// xxh3Bytes coerces a let-go byte-array or String arg to raw bytes.
func xxh3Bytes(v vm.Value) ([]byte, bool) { return asBytes(v) }

// xxh3Seed coerces a let-go integer seed to uint64 (matches the reflect path's
// Int(v.Uint()) round-trip; let-go Int is 64-bit, as Int.Hash's uint64(l) attests).
func xxh3Seed(v vm.Value) (uint64, bool) {
	if n, ok := v.(vm.Int); ok {
		return uint64(int64(n)), true
	}
	return 0, false
}

func installXxh3NS() {
	ns := vm.NewNamespace("xxh3")

	hash, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		b, ok := xxh3Bytes(vs[0])
		if !ok {
			return vm.NIL, vm.NewTypeError(vs[0], "is not a byte-array/String for", vm.NativeFnType)
		}
		return vm.Int(int64(xxh3.Hash(b))), nil
	})
	ns.Def("Hash", hash)

	hashSeed, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		b, ok := xxh3Bytes(vs[0])
		if !ok {
			return vm.NIL, vm.NewTypeError(vs[0], "is not a byte-array/String for", vm.NativeFnType)
		}
		seed, ok := xxh3Seed(vs[1])
		if !ok {
			return vm.NIL, vm.NewTypeError(vs[1], "is not an integer seed for", vm.NativeFnType)
		}
		return vm.Int(int64(xxh3.HashSeed(b, seed))), nil
	})
	ns.Def("HashSeed", hashSeed)

	hashString, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		s, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, vm.NewTypeError(vs[0], "is not a String for", vm.NativeFnType)
		}
		return vm.Int(int64(xxh3.HashString(string(s)))), nil
	})
	ns.Def("HashString", hashString)

	hashStringSeed, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		s, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, vm.NewTypeError(vs[0], "is not a String for", vm.NativeFnType)
		}
		seed, ok := xxh3Seed(vs[1])
		if !ok {
			return vm.NIL, vm.NewTypeError(vs[1], "is not an integer seed for", vm.NativeFnType)
		}
		return vm.Int(int64(xxh3.HashStringSeed(string(s), seed))), nil
	})
	ns.Def("HashStringSeed", hashStringSeed)

	RegisterNS(ns)
}

func init() { RegisterInstaller(installXxh3NS) }
