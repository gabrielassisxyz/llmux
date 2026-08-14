package catalog

import (
	"reflect"
	"sync"
	"testing"
)

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

func TestResolveRejectsNonExactSpellings(t *testing.T) {
	for _, alias := range []string{
		"kimi-k2.7 ",   // trailing space
		" kimi-k2.7",   // leading space
		"Kimi-K2.7",    // case-shifted base alias
		"KIMI-K2.7-K1", // case-shifted pinned alias
		"",             // empty
	} {
		if _, found := Resolve(alias); found {
			t.Errorf("Resolve(%q) found an alias that is not an exact catalog spelling", alias)
		}
	}
}

// TestResolveReturnsCompleteRouteForEveryGeneratedAlias proves the
// acceptance criterion directly: every one of the 28 generated aliases
// resolves through Resolve, not just through BaseRoutes or Routes, to its
// fixed upstream model, its eligible account set, its injection or the
// absence of one, and the base alias a pinned variant's suffix names. The
// expected values are written independently of internal/catalog's own
// baseRoutes table so a typo there cannot pass by agreeing with itself.
func TestResolveReturnsCompleteRouteForEveryGeneratedAlias(t *testing.T) {
	type want struct {
		model     string
		injection *Injection
	}
	bases := map[string]want{
		"kimi-k2.7":             {model: "kimi-k2.7-code:cloud"},
		"kimi-k2.6":             {model: "kimi-k2.6:cloud"},
		"glm-5.2":               {model: "glm-5.2:cloud"},
		"glm-5.1":               {model: "glm-5.1:cloud"},
		"deepseek-v4-pro-max":   {model: "deepseek-v4-pro:cloud", injection: &Injection{Field: "reasoning_effort", Value: "max"}},
		"deepseek-v4-pro-high":  {model: "deepseek-v4-pro:cloud", injection: &Injection{Field: "reasoning_effort", Value: "high"}},
		"deepseek-v4-flash-max": {model: "deepseek-v4-flash:cloud"},
	}

	resolved := 0
	for baseAlias, expect := range bases {
		route, found := Resolve(baseAlias)
		if !found {
			t.Fatalf("Resolve(%q) not found", baseAlias)
		}
		resolved++
		assertResolvedRoute(t, baseAlias, route, expect.model, baseAlias, []Account{AccountK1, AccountK2, AccountK3}, expect.injection)

		for _, account := range []Account{AccountK1, AccountK2, AccountK3} {
			pinnedAlias := baseAlias + "-" + string(account)
			pinned, found := Resolve(pinnedAlias)
			if !found {
				t.Fatalf("Resolve(%q) not found", pinnedAlias)
			}
			resolved++
			assertResolvedRoute(t, pinnedAlias, pinned, expect.model, baseAlias, []Account{account}, expect.injection)
		}
	}

	if resolved != 28 {
		t.Fatalf("resolved %d aliases through Resolve, want 28", resolved)
	}
}

func assertResolvedRoute(t *testing.T, alias string, route Route, wantModel, wantBaseAlias string, wantAccounts []Account, wantInjection *Injection) {
	t.Helper()
	if route.Alias != alias {
		t.Errorf("%s: Alias = %q, want %q", alias, route.Alias, alias)
	}
	if route.UpstreamModel != wantModel {
		t.Errorf("%s: UpstreamModel = %q, want %q", alias, route.UpstreamModel, wantModel)
	}
	if route.BaseAlias != wantBaseAlias {
		t.Errorf("%s: BaseAlias = %q, want %q", alias, route.BaseAlias, wantBaseAlias)
	}
	if !reflect.DeepEqual(route.EligibleAccounts, wantAccounts) {
		t.Errorf("%s: EligibleAccounts = %v, want %v", alias, route.EligibleAccounts, wantAccounts)
	}
	if !sameInjection(route.Injection, wantInjection) {
		t.Errorf("%s: Injection = %#v, want %#v", alias, route.Injection, wantInjection)
	}
}

// TestResolveIsSafeForConcurrentUse proves the lookup holds under go test
// -race: Resolve reads the fixed catalog and allocates fresh slices on
// every call, so concurrent callers, including ones resolving an unknown
// alias, must never observe a torn or shared result.
func TestResolveIsSafeForConcurrentUse(t *testing.T) {
	aliases := []string{"kimi-k2.7", "kimi-k2.7-k1", "glm-5.2-k2", "deepseek-v4-pro-max-k3", "unknown-alias"}

	const goroutines = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		alias := aliases[i%len(aliases)]
		go func(alias string) {
			defer wg.Done()
			route, found := Resolve(alias)
			if alias == "unknown-alias" {
				if found {
					t.Errorf("Resolve(%q) unexpectedly found a route", alias)
				}
				return
			}
			if !found {
				t.Errorf("Resolve(%q) not found", alias)
				return
			}
			if route.Alias != alias {
				t.Errorf("Resolve(%q) returned Alias = %q", alias, route.Alias)
			}
		}(alias)
	}
	wg.Wait()
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
