package main

import "testing"

func TestParseOptionsUsesSafeDefaultsAndExplicitOverrides(t *testing.T) {
	defaults, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Listen != "127.0.0.1:8082" || defaults.AllowUnauthenticated ||
		defaults.BackendMode != "virtual" || defaults.MaxBodyBytes != 4<<20 ||
		defaults.RequestsPerSecond != 20 || defaults.RequestBurst != 40 {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	overridden, err := parseOptions([]string{
		"-listen=0.0.0.0:9000",
		"-allow-unauthenticated",
		"-backend=rooted",
		"-workspace-root=/workspace",
		"-max-body-bytes=1024",
		"-requests-per-second=2.5",
		"-request-burst=3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Listen != "0.0.0.0:9000" || !overridden.AllowUnauthenticated ||
		overridden.BackendMode != "rooted" || overridden.WorkspaceRoot != "/workspace" ||
		overridden.MaxBodyBytes != 1024 || overridden.RequestsPerSecond != 2.5 ||
		overridden.RequestBurst != 3 {
		t.Fatalf("unexpected overrides: %+v", overridden)
	}
}

func TestParseOptionsRejectsAmbiguousOrUnsafeValues(t *testing.T) {
	for name, arguments := range map[string][]string{
		"unknown flag":      {"-unknown"},
		"positional":        {"extra"},
		"listen":            {"-listen="},
		"idempotency":       {"-idempotency-dir="},
		"backend":           {"-backend=unknown"},
		"root":              {"-backend=rooted"},
		"sampling":          {"-trace-sample-ratio=2"},
		"body":              {"-max-body-bytes=0"},
		"rate":              {"-requests-per-second=0"},
		"burst":             {"-request-burst=0"},
		"malformed numeric": {"-request-burst=not-a-number"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOptions(arguments); err == nil {
				t.Fatal("parseOptions accepted invalid arguments")
			}
		})
	}
}
