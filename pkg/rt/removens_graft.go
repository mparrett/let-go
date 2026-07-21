package rt

// GRAFT (throwaway, let-go#597): RemoveNS absent at v1.8.0; suite harness needs it.
func RemoveNS(name string) {
	canonical := resolveNSAlias(name)
	delete(nsRegistry, canonical)
	delete(nsNeedsLoad, canonical)
}
