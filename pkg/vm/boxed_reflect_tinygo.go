//go:build tinygo

/*
 * TinyGo does not implement reflect.Type.Method, so boxed Go values
 * cannot expose their method tables to Clojure code under TinyGo.
 * The VM still works; method calls on boxed values just won't resolve.
 */

package vm

import "reflect"

func reflectMethods(t reflect.Type) map[Symbol]*NativeFn { return nil }
