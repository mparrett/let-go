package vm

import "testing"

func TestVarMetadata(t *testing.T) {
	v := NewVar(NewNamespace("test.var-meta"), "test.var-meta", "x")
	if got := v.Meta(); got != NIL {
		t.Fatalf("new var meta = %v, want nil", got)
	}

	meta := NewPersistentMap([]Value{Keyword("doc"), String("the doc")})
	v.SetMeta(meta)
	if got := v.Meta(); got != meta {
		t.Fatalf("var meta = %v, want %v", got, meta)
	}
}
