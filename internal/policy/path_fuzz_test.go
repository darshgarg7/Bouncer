package policy

import (
	"strings"
	"testing"
)

func FuzzVirtualPathNormalization(f *testing.F) {
	for _, seed := range []string{
		"workspace/file.txt",
		"workspace/../secret",
		"/etc/passwd",
		`workspace\file.txt`,
		"workspace//file.txt",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		first, firstValid := normalizeVirtualPath(value)
		second, secondValid := normalizeVirtualPath(value)
		if first != second || firstValid != secondValid {
			t.Fatalf("normalization is nondeterministic for %q", value)
		}
		if !firstValid {
			return
		}
		if first == "" || strings.HasPrefix(first, "/") || strings.Contains(first, `\`) {
			t.Fatalf("accepted unsafe normalized path %q from %q", first, value)
		}
		for _, component := range strings.Split(first, "/") {
			if component == "" || component == "." || component == ".." {
				t.Fatalf("accepted unsafe path component %q in %q", component, first)
			}
		}
		if pathWithin(first, "workspace") !=
			(first == "workspace" || strings.HasPrefix(first, "workspace/")) {
			t.Fatalf("path containment disagrees for %q", first)
		}
	})
}
