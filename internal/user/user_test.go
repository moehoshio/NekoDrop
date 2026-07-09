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

// TestMigrationLifecycle exercises enable, lookup, regenerate and disable of
// account-migration codes.
func TestMigrationLifecycle(t *testing.T) {
	r := NewRegistry()
	u := r.Create("Alice")

	if r.MigrationCode(u.Token) != "" {
		t.Fatal("migration must be off by default")
	}
	if r.ByMigrationCode("") != nil {
		t.Fatal("empty code must never resolve")
	}

	code, ok := r.EnableMigration(u.Token)
	if !ok || code == "" {
		t.Fatalf("enable = (%q, %v)", code, ok)
	}
	if got := r.ByMigrationCode(code); got == nil || got.UID != u.UID {
		t.Fatalf("ByMigrationCode = %+v, want user %s", got, u.UID)
	}

	// Regenerating invalidates the old code.
	code2, _ := r.EnableMigration(u.Token)
	if code2 == code {
		t.Fatal("regenerated code should differ")
	}
	if r.ByMigrationCode(code) != nil {
		t.Fatal("old code should be invalid after regeneration")
	}

	// Disabling invalidates the current code.
	if !r.DisableMigration(u.Token) {
		t.Fatal("disable should succeed for a known token")
	}
	if r.ByMigrationCode(code2) != nil || r.MigrationCode(u.Token) != "" {
		t.Fatal("code should be invalid after disabling")
	}

	// Unknown tokens are rejected.
	if _, ok := r.EnableMigration("nope"); ok {
		t.Fatal("enable with unknown token should fail")
	}
	if r.DisableMigration("nope") {
		t.Fatal("disable with unknown token should fail")
	}
}

// TestMigrationProtectsFromEviction verifies an unnamed user who opted into
// migration outlives plain anonymous identities under memory pressure.
func TestMigrationProtectsFromEviction(t *testing.T) {
	r := NewRegistryWithStore(nil, 3)
	keeper := r.Create("") // unnamed, but opts into migration
	code, _ := r.EnableMigration(keeper.Token)
	r.Create("")
	r.Create("")
	r.Create("")
	r.Create("")

	if r.Get(keeper.Token) == nil {
		t.Fatal("migration-enabled user should be preserved over plain anonymous users")
	}
	if got := r.ByMigrationCode(code); got == nil || got.UID != keeper.UID {
		t.Fatalf("migration code lost during eviction: %+v", got)
	}
}

// TestAutoNameAndNamedFlag verifies that a visitor who never chose a name is
// assigned a random one (never empty, never a bare "anonymous" state) and that
// the Named flag tracks whether the current name was deliberately chosen.
func TestAutoNameAndNamedFlag(t *testing.T) {
	r := NewRegistry()
	u := r.Create("")
	if u.Name == "" || u.Named {
		t.Fatalf("unnamed user = {%q %v}, want a generated name and Named=false", u.Name, u.Named)
	}
	renamed, ok := r.Rename(u.Token, "  Bob  ")
	if !ok || renamed.Name != "Bob" || !renamed.Named {
		t.Fatalf("rename = {%q %v ok=%v}, want {Bob true true}", renamed.Name, renamed.Named, ok)
	}
	// Clearing the name assigns a fresh generated one and the unnamed state.
	reverted, _ := r.Rename(u.Token, "   ")
	if reverted.Name == "" || reverted.Name == "Bob" || reverted.Named {
		t.Fatalf("revert = {%q %v}, want a fresh generated name and Named=false", reverted.Name, reverted.Named)
	}
}

// TestRandomNameShape guards that generated names are non-empty, sane display
// names that survive the sanitizer unchanged.
func TestRandomNameShape(t *testing.T) {
	for i := 0; i < 50; i++ {
		n := RandomName()
		if n == "" || SanitizeName(n) != n {
			t.Fatalf("RandomName() = %q, want a sanitizer-stable non-empty name", n)
		}
	}
}
