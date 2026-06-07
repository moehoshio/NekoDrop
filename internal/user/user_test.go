package user

import "testing"

// TestRegistryEvictsToStayBounded verifies the identity registry caps memory by
// evicting the oldest unnamed users while preserving named ones.
func TestRegistryEvictsToStayBounded(t *testing.T) {
	r := NewRegistryWithStore(nil, 3)

	named := r.Create("Alice") // should survive eviction (named)
	r.Create("")               // unnamed
	r.Create("")               // unnamed
	// Creating more forces eviction of the oldest unnamed users.
	r.Create("")
	r.Create("")

	if len(r.byToken) > 3 {
		t.Fatalf("registry size = %d, want <= 3", len(r.byToken))
	}
	if r.Get(named.Token) == nil {
		t.Fatal("named user should be preserved over unnamed users")
	}
}

// TestDefaultNameAndNamedFlag verifies the unnamed default and the Named flag.
func TestDefaultNameAndNamedFlag(t *testing.T) {
	r := NewRegistry()
	u := r.Create("")
	if u.Name != DefaultName || u.Named {
		t.Fatalf("unnamed user = {%q %v}, want {%q false}", u.Name, u.Named, DefaultName)
	}
	renamed, ok := r.Rename(u.Token, "  Bob  ")
	if !ok || renamed.Name != "Bob" || !renamed.Named {
		t.Fatalf("rename = {%q %v ok=%v}, want {Bob true true}", renamed.Name, renamed.Named, ok)
	}
	// Clearing the name reverts to the default, unnamed state.
	reverted, _ := r.Rename(u.Token, "   ")
	if reverted.Name != DefaultName || reverted.Named {
		t.Fatalf("revert = {%q %v}, want {%q false}", reverted.Name, reverted.Named, DefaultName)
	}
}
