package catalog

import "testing"

func TestRoutesGenerateExactlyThreePinnedVariantsPerBaseRoute(t *testing.T) {
	routes := Routes()
	if len(routes) != 28 {
		t.Fatalf("Routes() returned %d entries, want 28", len(routes))
	}

	for _, base := range BaseRoutes() {
		for _, account := range []Account{AccountK1, AccountK2, AccountK3} {
			alias := base.Alias + "-" + string(account)
			variant, found := Resolve(alias)
			if !found {
				t.Errorf("Resolve(%q) did not find a generated variant", alias)
				continue
			}
			if variant.UpstreamModel != base.UpstreamModel {
				t.Errorf("%s model = %q, want inherited %q", alias, variant.UpstreamModel, base.UpstreamModel)
			}
			if len(variant.EligibleAccounts) != 1 || variant.EligibleAccounts[0] != account {
				t.Errorf("%s eligible accounts = %v, want [%s]", alias, variant.EligibleAccounts, account)
			}
			if !sameInjection(variant.Injection, base.Injection) {
				t.Errorf("%s injection = %#v, want inherited %#v", alias, variant.Injection, base.Injection)
			}
		}
	}
}

func TestResolveDoesNotParseUnregisteredPinnedSuffixes(t *testing.T) {
	for _, alias := range []string{"kimi-k2.7-k4", "kimi-k2.7-k1x", "kimi-k2.7-", "unknown-k1"} {
		if _, found := Resolve(alias); found {
			t.Errorf("Resolve(%q) found an alias that was not generated", alias)
		}
	}
}

func TestPinnedAliasesAreExactCatalogNames(t *testing.T) {
	for _, route := range Routes() {
		if hasPinnedSuffix(route.Alias) && len(route.EligibleAccounts) != 1 {
			t.Errorf("pinned alias %q has %d eligible accounts", route.Alias, len(route.EligibleAccounts))
		}
	}
}

func hasPinnedSuffix(alias string) bool {
	for _, account := range []Account{AccountK1, AccountK2, AccountK3} {
		if len(alias) > len(account)+1 && alias[len(alias)-len(account)-1:] == "-"+string(account) {
			return true
		}
	}
	return false
}

func sameInjection(left, right *Injection) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
