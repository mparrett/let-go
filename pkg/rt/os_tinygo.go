//go:build tinygo

/*
 * TinyGo stub: the os namespace uses reflect.Type.IsVariadic via Box() to
 * register os.Getenv / exec.Command / os.TempDir, and TinyGo's reflect
 * does not implement IsVariadic. Browser wasm has no meaningful "os"
 * surface anyway. Helpers shared with system.go are kept here.
 */

package rt

import "github.com/nooga/let-go/pkg/vm"

func installOsNS() {}

func lineSeparator() string { return "\n" }

func mustWrap(fn func([]vm.Value) (vm.Value, error)) vm.Value {
	v, err := vm.NativeFnType.Wrap(fn)
	if err != nil {
		panic(err)
	}
	return v
}
