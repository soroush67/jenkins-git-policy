package policy

import "testing"

func TestGlobMatching(t *testing.T) {
	cases := []struct {
		glob  string
		fold  bool
		match []string
		miss  []string
	}{
		{"*.dll", true, []string{"a.dll", "A.DLL"}, []string{"lib/a.dll", "a.dll.txt"}},
		{"**/*.dll", true, []string{"a.dll", "lib/a.dll", "x/y/z/Mic.Caching.DLL"}, []string{"a.dllx"}},
		{"**/obj/**", true, []string{"obj/a.o", "src/obj/x/y.cache"}, []string{"obj", "objects/a", "src/obj"}},
		{"**/bin/Debug/**", true, []string{"app/bin/debug/app.exe"}, []string{"app/bin/Release/app.exe"}},
		{"vendor/VendorSdk/*.dll", true, []string{"vendor/vendorsdk/Sdk.dll"}, []string{"vendor/VendorSdk/x/Sdk.dll"}},
		{"a/**/b", false, []string{"a/b", "a/x/b", "a/x/y/b"}, []string{"a/xb", "ab"}},
		{"refs/heads/release/*", false, []string{"refs/heads/release/1.0"}, []string{"refs/heads/release/1.0/hotfix", "refs/heads/Release/1.0"}},
		{"refs/heads/release/**", false, []string{"refs/heads/release/1.0", "refs/heads/release/1.0/hotfix"}, []string{"refs/heads/release"}},
		{"refs/tags/v?.*", false, []string{"refs/tags/v1.0"}, []string{"refs/tags/v10.0"}},
		{"**", false, []string{"a", "a/b"}, []string{""}},
	}
	for _, c := range cases {
		re, err := CompileGlob(c.glob, c.fold)
		if err != nil {
			t.Fatalf("CompileGlob(%q): %v", c.glob, err)
		}
		for _, s := range c.match {
			if !re.MatchString(s) {
				t.Errorf("%q should match %q (regexp %s)", c.glob, s, re)
			}
		}
		for _, s := range c.miss {
			if re.MatchString(s) {
				t.Errorf("%q should not match %q (regexp %s)", c.glob, s, re)
			}
		}
	}
}

func TestGlobSyntaxErrors(t *testing.T) {
	for _, g := range []string{"", "/abs", "trailing/", "a//b", "a/../b", "./a", "a***", "a**b/c", "[ab].dll", "{a,b}", `a\b`} {
		if err := CheckGlob(g); err == nil {
			t.Errorf("CheckGlob(%q) accepted", g)
		}
	}
}
