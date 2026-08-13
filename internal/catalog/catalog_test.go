package catalog

import "testing"

func TestBaseRoutesMatchTheFixedCatalog(t *testing.T) {
	routes := BaseRoutes()
	wantModels := map[string]string{
		"kimi-k2.7":             "kimi-k2.7-code:cloud",
		"kimi-k2.6":             "kimi-k2.6:cloud",
		"glm-5.2":               "glm-5.2:cloud",
		"glm-5.1":               "glm-5.1:cloud",
		"deepseek-v4-pro-max":   "deepseek-v4-pro:cloud",
		"deepseek-v4-pro-high":  "deepseek-v4-pro:cloud",
		"deepseek-v4-flash-max": "deepseek-v4-flash:cloud",
	}
	if len(routes) != len(wantModels) {
		t.Fatalf("BaseRoutes() returned %d routes, want %d", len(routes), len(wantModels))
	}
	seenAliases := make(map[string]bool, len(routes))
	for _, route := range routes {
		wantModel, knownAlias := wantModels[route.Alias]
		if !knownAlias {
			t.Errorf("unexpected alias %q", route.Alias)
			continue
		}
		if seenAliases[route.Alias] {
			t.Errorf("duplicate alias %q", route.Alias)
		}
		seenAliases[route.Alias] = true
		if route.UpstreamModel != wantModel {
			t.Errorf("%s upstream model = %q, want %q", route.Alias, route.UpstreamModel, wantModels[route.Alias])
		}
		if route.EligibleAccounts != [3]Account{AccountK1, AccountK2, AccountK3} {
			t.Errorf("%s eligible accounts = %v", route.Alias, route.EligibleAccounts)
		}
	}
	for alias := range wantModels {
		if !seenAliases[alias] {
			t.Errorf("missing alias %q", alias)
		}
	}
}

func TestBaseRoutesAllowOnlyReasoningEffortInjections(t *testing.T) {
	wantInjections := map[string]string{"deepseek-v4-pro-max": "max", "deepseek-v4-pro-high": "high"}
	for _, route := range BaseRoutes() {
		wantValue, injects := wantInjections[route.Alias]
		if !injects && route.Injection != nil {
			t.Errorf("%s unexpectedly injects %q", route.Alias, route.Injection.Field)
			continue
		}
		if injects && (route.Injection == nil || route.Injection.Field != "reasoning_effort" || route.Injection.Value != wantValue) {
			t.Errorf("%s injection = %#v", route.Alias, route.Injection)
		}
	}
}

func TestBaseRoutesReturnsIndependentInjectionValues(t *testing.T) {
	first := BaseRoutes()
	first[4].Injection.Value = "changed"
	second := BaseRoutes()
	if second[4].Injection.Value != "max" {
		t.Fatalf("BaseRoutes() retained caller mutation: %q", second[4].Injection.Value)
	}
}
