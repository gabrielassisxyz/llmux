package catalog

import (
	"strings"
	"testing"
)

func TestValidate_Success(t *testing.T) {
	err := Validate(BaseRoutes(), Routes())
	if err != nil {
		t.Fatalf("expected fixed catalog to pass validation, got: %v", err)
	}
}

func TestValidate_Failures(t *testing.T) {
	// A helper to create a valid base/all set and mutate it
	setup := func(mutate func(base []Route, all []Route) ([]Route, []Route)) ([]Route, []Route) {
		base := BaseRoutes()
		all := Routes()
		return mutate(base, all)
	}

	tests := []struct {
		name          string
		setup         func() ([]Route, []Route)
		expectedError string
	}{
		{
			name: "wrong number of base routes",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					base = append(base, Route{Alias: "extra"})
					return base, all
				})
			},
			expectedError: "expected exactly 7 base routes",
		},
		{
			name: "wrong number of public aliases",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					all = append(all, Route{Alias: "extra"})
					return base, all
				})
			},
			expectedError: "expected exactly 28 public aliases",
		},
		{
			name: "duplicate public aliases",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					all[0].Alias = all[1].Alias
					return base, all
				})
			},
			expectedError: "duplicate public alias found",
		},
		{
			name: "empty upstream model",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					all[0].UpstreamModel = ""
					return base, all
				})
			},
			expectedError: "empty upstream model",
		},
		{
			name: "injection targets messages",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					all[0].Injection = &Injection{Field: "messages", Value: "something"}
					return base, all
				})
			},
			expectedError: "injection targets messages",
		},
		{
			name: "base route does not reference 3 accounts",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					base[0].EligibleAccounts = []Account{AccountK1, AccountK2}
					return base, all
				})
			},
			expectedError: "does not reference exactly 3 accounts",
		},
		{
			name: "base route has invalid account",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					base[0].EligibleAccounts = []Account{AccountK1, AccountK2, "k9"}
					return base, all
				})
			},
			expectedError: "invalid account",
		},
		{
			name: "base route duplicates account",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					base[0].EligibleAccounts = []Account{AccountK1, AccountK2, AccountK2}
					return base, all
				})
			},
			expectedError: "does not reference all three accounts exactly once",
		},
		{
			name: "pinned route does not reference 1 account",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					// find a pinned route (len != 3)
					for i, r := range all {
						if len(r.EligibleAccounts) != 3 {
							all[i].EligibleAccounts = []Account{AccountK1, AccountK2}
							break
						}
					}
					return base, all
				})
			},
			expectedError: "does not reference exactly one account",
		},
		{
			name: "wrong reasoning_effort injection count",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					for i, r := range base {
						if r.Alias == "kimi-k2.7" {
							base[i].Injection = &Injection{Field: "reasoning_effort", Value: "max"}
							break
						}
					}
					return base, all
				})
			},
			expectedError: "route kimi-k2.7 injects reasoning_effort but is not one of the declared pro routes",
		},
		{
			name: "pinned route does not match base upstream model",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					for i, r := range all {
						if len(r.EligibleAccounts) == 1 {
							all[i].UpstreamModel = "wrong-model"
							break
						}
					}
					return base, all
				})
			},
			expectedError: "has upstream model",
		},
		{
			name: "pinned route injection mismatch",
			setup: func() ([]Route, []Route) {
				return setup(func(base []Route, all []Route) ([]Route, []Route) {
					for i, r := range all {
						if len(r.EligibleAccounts) == 1 {
							// if base had an injection, we remove it, or if it didn't, we add one
							if all[i].Injection == nil {
								all[i].Injection = &Injection{Field: "reasoning_effort", Value: "max"}
							} else {
								all[i].Injection = nil
							}
							break
						}
					}
					return base, all
				})
			},
			expectedError: "injection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, a := tt.setup()
			err := Validate(b, a)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.expectedError)
			}
			if !strings.Contains(err.Error(), tt.expectedError) {
				t.Fatalf("expected error containing %q, got %q", tt.expectedError, err.Error())
			}
		})
	}
}
