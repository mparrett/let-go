# Repro + experiment for nooga/let-go#357

Float parameter constrained only by float arithmetic → non-compiling `float64 + int`.

`repro.lg` holds the two kernels from the issue (`add-to-float`, `escape`).

## Lower it

```
./lg scripts/lg-compile out github.com/nooga/let-go/out repro/357-float-param/repro.lg
```

## `main` (the bug)

```go
func add_to_float(ec *vm.ExecContext, arg0 int) float64 {   // param: int
	var z float64                                           // accumulator: native float64
	...
	z = z + arg0                                            // float64 + int -> won't compile
}
```
`go build`: `invalid operation: z + arg0 (mismatched types float64 and int)`.

## With the fixpoint experiment on this branch

This branch also applies a candidate back-propagation: make `infer-arg-types`
constrain a param `:float` when it's used in float arithmetic, and iterate
`typeinfer ↔ infer-arg-types` to a fixpoint so the constraint reaches the param
(`pkg/rt/core/ir/passes/{infer_arg_types,pipeline}.lg`).

```go
func add_to_float(ec *vm.ExecContext, arg0 vm.Value) vm.Value {  // param: vm.Value
	var z vm.Value                                               // accumulator: BOXED now
	...
	z = rt.AddValue(z, arg0)                                     // compiles, but boxed
}
```

`go build` succeeds. **But** the param widened to `vm.Value` instead of `float64`,
and that pulled the accumulator `z` from native `float64` to boxed `vm.Value` — the
whole loop is now boxed (`rt.AddValue`). Same for `escape` (`cx`/`cy` → `vm.Value`).

## Why it stops at boxed, not `float64`

`a` can be called with an int (`(add-to-float 3)` is valid; `float + int → float`),
so the sound inference for the param is int-or-float → boxed, not `float64`. Landing
on `float64` would need call-site analysis to prove float-only.

## Takeaway

Widening the param fixes the *compile error* but by boxing the loop — it loses the
native `float64` path that motivates the issue. The issue's second option — emit an
`int → float64` coercion at the use site (keep the param, coerce where it meets a
float, `z` stays `float64`) — is the one that preserves the native loop. **This branch
is a demonstration of the widening tradeoff, not a proposed fix.**
