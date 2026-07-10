/*
 * Copyright (c) 2026 Norman Nunley, Jr <nnunley@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package ir_test

// Phase 1 of cross-ns typed direct calls: the EMIT machinery. A *lowered-registry*
// entry for a fn in ANOTHER lowered namespace (:go-pkg set, :native? false) lowers
// a call to `pkg.Fn(ec, args)` — package-qualified like a native module call, but
// ec-passing like an intra-ns lowered call — and records the callee package for
// import. (Phase 2 wires the corpus-wide registry that produces such entries.)

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nooga/let-go/pkg/rt"
	"github.com/nooga/let-go/pkg/vm"
)

func TestCrossNsLoweredDirectCallEmit(t *testing.T) {
	ensureLoader()
	runLispExpr(t, `(do (create-ns (quote otherns)) (intern (quote otherns) (quote callee)))`)

	fn := buildLispIR(t, `(defn xcaller [x] (otherns/callee x))`)
	optimizeLispIR(t, fn)
	passVarCounter++
	varName := fmt.Sprintf("*xns-dc-fn-%d*", passVarCounter)
	rt.NS(rt.NameCoreNS).Def(varName, fn)

	// Hand-seed a cross-ns lowered entry (what Phase 2's corpus-wide registry
	// will build). Key = [internal-ns-sym("otherns") "callee" 1] = ['otherns ...].
	v := runLispExpr(t, fmt.Sprintf(`
	  (let [reg {(ir.lower-go/registry-key (quote otherns) "callee" 1)
	             {:go-name "Callee" :arity 1 :needs-error? false
	              :param-specs ["vm.Value"] :result-spec "vm.Value"
	              :native? false
	              :go-pkg "github.com/nooga/let-go/pkg/rt/core_go_lowered/otherns"}}]
	    (binding [ir.lower-go/*lowered-registry* reg
	              ir.lower-go/*native-imports-used* (atom #{})]
	      (ir.lower-go/lower %s :strict)))`, varName))
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		t.Fatalf("expected lower to return a map, got %T", v)
	}
	rendered := bindAndRenderGoDecl(t, m)

	if !strings.Contains(rendered, "otherns.Callee(ec,") {
		t.Fatalf("expected cross-ns lowered direct call otherns.Callee(ec, ...):\n--- go ---\n%s", rendered)
	}
	if strings.Contains(rendered, "InvokeValue") || strings.Contains(rendered, "CachedVarFn") {
		t.Fatalf("expected a direct call, but found a trampoline:\n--- go ---\n%s", rendered)
	}
}

// When a typed param can't be coerced, emit-typed-direct-call falls back to the
// trampoline. The callee's Go import must NOT be recorded in that case — a
// leaked, unused import makes the emitted file fail to compile. Regression for
// recording the import in lookup-direct-callable (before coercion) rather than
// after emission succeeds.
//
// An UNPROVEN vm.Value arg into a scalar param no longer falls back: it takes
// the guarded comma-ok path, which emits the static call and rightly records
// the import (see TestCrossNsDirectCallGuardedRecordsImport). What still bails
// whole-call is a PROVEN-mismatched scalar; here the arg is proven int
// ((+ x 1) forces the int constraint) against a "string" param.
func TestCrossNsDirectCallFallbackDoesNotLeakImport(t *testing.T) {
	ensureLoader()
	runLispExpr(t, `(do (create-ns (quote leakns)) (intern (quote leakns) (quote callee)))`)
	runLispExpr(t, `(def lg-test-leak-imports (atom #{}))`)

	fn := buildLispIR(t, `(defn lcaller [x] (leakns/callee (+ x 1)))`)
	optimizeLispIR(t, fn)
	passVarCounter++
	varName := fmt.Sprintf("*xns-leak-fn-%d*", passVarCounter)
	rt.NS(rt.NameCoreNS).Def(varName, fn)

	// Entry with a scalar "string" param: passes lookup's
	// supported-coercion-type? gate, but coerce-arg-to-type returns nil for
	// the proven-int arg (mismatched concrete scalar, no guard possible),
	// forcing the trampoline fallback.
	v := runLispExpr(t, fmt.Sprintf(`
	  (let [reg {(ir.lower-go/registry-key (quote leakns) "callee" 1)
	             {:go-name "Callee" :arity 1 :needs-error? false
	              :param-specs ["string"] :result-spec "vm.Value"
	              :native? false
	              :go-pkg "github.com/nooga/let-go/pkg/rt/core_go_lowered/leakns"}}]
	    (binding [ir.lower-go/*lowered-registry* reg
	              ir.lower-go/*native-imports-used* lg-test-leak-imports]
	      (ir.lower-go/lower %s :strict)))`, varName))
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		t.Fatalf("expected lower to return a map, got %T", v)
	}
	rendered := bindAndRenderGoDecl(t, m)

	// Sanity: the call really did fall back to the trampoline (otherwise the
	// import-empty assertion below would be vacuous).
	if !strings.Contains(rendered, "InvokeValue") && !strings.Contains(rendered, "CachedVarFn") {
		t.Fatalf("expected a trampoline fallback for an uncoercible string param:\n--- go ---\n%s", rendered)
	}
	// The fix: no import recorded when emission fell back.
	if imports := string(runLispExpr(t, `(pr-str (deref lg-test-leak-imports))`).(vm.String)); imports != "#{}" {
		t.Fatalf("fallback leaked an import: *native-imports-used* = %s, want #{}", imports)
	}
}

// The complement of the leak test: an unproven vm.Value arg into an int param
// takes the guarded comma-ok path — the static call IS emitted (inside the
// guard) alongside the trampoline fallback branch, so recording the callee's
// import is required for the file to compile.
func TestCrossNsDirectCallGuardedRecordsImport(t *testing.T) {
	ensureLoader()
	runLispExpr(t, `(do (create-ns (quote guardns)) (intern (quote guardns) (quote callee)))`)
	runLispExpr(t, `(def lg-test-guard-imports (atom #{}))`)

	fn := buildLispIR(t, `(defn gcaller [x] (guardns/callee x))`)
	optimizeLispIR(t, fn)
	passVarCounter++
	varName := fmt.Sprintf("*xns-guard-fn-%d*", passVarCounter)
	rt.NS(rt.NameCoreNS).Def(varName, fn)

	v := runLispExpr(t, fmt.Sprintf(`
	  (let [reg {(ir.lower-go/registry-key (quote guardns) "callee" 1)
	             {:go-name "Callee" :arity 1 :needs-error? false
	              :param-specs ["int"] :result-spec "vm.Value"
	              :native? false
	              :go-pkg "github.com/nooga/let-go/pkg/rt/core_go_lowered/guardns"}}]
	    (binding [ir.lower-go/*lowered-registry* reg
	              ir.lower-go/*native-imports-used* lg-test-guard-imports]
	      (ir.lower-go/lower %s :strict)))`, varName))
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		t.Fatalf("expected lower to return a map, got %T", v)
	}
	rendered := bindAndRenderGoDecl(t, m)

	if !strings.Contains(rendered, ".(vm.Int)") {
		t.Fatalf("expected a comma-ok vm.Int guard:\n--- go ---\n%s", rendered)
	}
	if !strings.Contains(rendered, "guardns.Callee(ec, int(") {
		t.Fatalf("expected the guarded static call guardns.Callee(ec, int(...)):\n--- go ---\n%s", rendered)
	}
	if !strings.Contains(rendered, "InvokeValue") && !strings.Contains(rendered, "CachedVarFn") {
		t.Fatalf("expected a trampoline fallback branch:\n--- go ---\n%s", rendered)
	}
	if imports := string(runLispExpr(t, `(pr-str (deref lg-test-guard-imports))`).(vm.String)); !strings.Contains(imports, "guardns") {
		t.Fatalf("guarded emission must record the callee import, got %s", imports)
	}
}
