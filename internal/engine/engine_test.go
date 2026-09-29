package engine

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/soroush67/git-policy/internal/policy"
)

var now = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

const (
	zero = "0000000000000000000000000000000000000000"
	oid  = "c6dd86fbc422e0d233a89e44a229f82181aa2a6c"
)

func compile(t *testing.T, src string) *policy.Compiled {
	t.Helper()
	c, fs := policy.Build([]byte(src), policy.Options{Now: now})
	if c == nil {
		t.Fatalf("invalid policy: %v", fs)
	}
	return c
}

func example(t *testing.T) *Engine {
	t.Helper()
	src, err := os.ReadFile("../../examples/policy.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return New(compile(t, string(src)))
}

func fresh(groups ...string) Membership {
	return Membership{Available: true, GrantsUsable: true, Groups: groups, State: "fresh"}
}

func expired(groups ...string) Membership {
	return Membership{Available: true, GrantsUsable: false, Groups: groups, State: "expired"}
}

func push(user, project, ref string, m Membership) Request {
	return Request{User: user, Project: project, Membership: m, Now: now,
		Updates: []RefUpdate{{Old: zero, New: oid, Ref: ref}}}
}

func codes(d *Decision) string {
	var out []string
	for _, v := range d.Violations {
		out = append(out, v.Code)
	}
	return strings.Join(out, ",")
}

// Worked examples of PHASE-2-SCHEMA.md §7 that concern identity.
func TestWorkedExamplesIdentity(t *testing.T) {
	e := example(t)
	cases := []struct {
		name string
		req  Request
		want string // "" = accept, else violation codes
	}{
		{"E01 alex denied on payment-api", push("alex", "finance/payment-api", "refs/heads/main", fresh()), policy.UserPushDenied},
		{"E01 case-insensitive user and project", push("Alex", "Finance/Payment-API", "refs/heads/main", fresh()), policy.UserPushDenied},
		{"E02 alex allowed on reporting", push("alex", "finance/reporting", "refs/heads/main", fresh()), ""},
		{"E03 contractor allowed in outsourcing", push("carol", "outsourcing/portal", "refs/heads/main", fresh("contractors")), ""},
		{"E03 nested project in outsourcing", push("carol", "outsourcing/team/app", "refs/heads/main", fresh("contractors")), ""},
		{"E04 contractor denied elsewhere", push("carol", "finance/accounting", "refs/heads/main", fresh("contractors")), policy.GroupPushDenied},
		{"E05 expired cache: group allow ignored", push("carol", "outsourcing/portal", "refs/heads/main", expired("contractors")), policy.GroupPushDenied},
		{"E22 mandatory user deny", push("terminated.user", "sandbox/playground", "refs/heads/x", fresh()), policy.MandatoryUserDenied},
		{"unrelated user, no rules", push("bob", "finance/accounting", "refs/heads/main", fresh()), ""},
		{"namespace prefix is component-wise", push("carol", "outsourcingx/app", "refs/heads/main", fresh("contractors")), policy.GroupPushDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := codes(e.Evaluate(c.req)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestPrecedence(t *testing.T) {
	e := New(compile(t, `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
users:
  alex:
    push: deny
    refs: {"refs/heads/main": deny}
    projects:
      a/b: {push: allow}
      a/c: {push: allow, refs: {"refs/heads/release/*": deny}}
  bob:
    projects:
      a/b: {push: allow}
  dave:
    namespaces:
      a: {push: deny}
      a/deep: {push: allow}
groups:
  devs:
    projects:
      a/b: {push: deny}
  ops:
    namespaces:
      a: {push: allow}
  qa:
    namespaces:
      a: {push: deny}
`))
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"global user deny", push("alex", "x/y", "refs/heads/dev", fresh()), policy.UserPushDenied},
		{"project allow beats global and global+ref deny (scope dominates)", push("alex", "a/b", "refs/heads/main", fresh()), ""},
		{"project+ref deny beats project allow", push("alex", "a/c", "refs/heads/release/1", fresh()), policy.UserPushDenied},
		{"project allow, other ref", push("alex", "a/c", "refs/heads/feature", fresh()), ""},
		{"user allow beats group deny at the same level", push("bob", "a/b", "refs/heads/main", fresh("devs")), ""},
		{"group deny when no user rule", push("carol", "a/b", "refs/heads/main", fresh("devs")), policy.GroupPushDenied},
		{"group deny beats group allow at the same level", push("erin", "a/x", "refs/heads/main", fresh("ops", "qa")), policy.GroupPushDenied},
		{"deeper namespace wins", push("dave", "a/deep/p", "refs/heads/main", fresh()), ""},
		{"shallower namespace applies", push("dave", "a/other/p", "refs/heads/main", fresh()), policy.UserPushDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := codes(e.Evaluate(c.req)); got != c.want {
				t.Fatalf("got %q, want %q\n%+v", got, c.want, e.TraceIdentity(NormalizeUser(c.req.User), c.req.Membership.Groups, true, c.req.Project, c.req.Updates[0].Ref, "all"))
			}
		})
	}
}

func TestMandatoryAndExceptions(t *testing.T) {
	e := New(compile(t, `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
mandatory:
  deny_users: ["@unknown", gone]
  deny_groups: [blocked]
users:
  gone: {push: allow}
  alex:
    push: deny
settings:
  limits: {max_ref_updates: 2}
exceptions:
  - id: EXC-ALEX-HOTFIX
    rules: [USER_PUSH_DENIED]
    subjects: {users: [alex]}
    scope: {projects: [a/b], refs: ["refs/heads/hotfix/*"]}
    reason: hotfix access for the incident
    expires: 2026-10-10
  - id: EXC-EXPIRED
    rules: [USER_PUSH_DENIED]
    subjects: {users: [alex]}
    scope: {projects: [a/old]}
    reason: expired exception kept around
    expires: 2026-09-01
  - id: EXC-GONE-NONMANDATORY
    rules: [MANDATORY_USER_DENIED]
    subjects: {users: [gone]}
    reason: missing the mandatory flag so it never applies
    expires: 2026-10-10
  - id: EXC-BULK
    rules: [LIMIT_REF_UPDATES]
    subjects: {users: [svc-migration]}
    reason: bulk tag import during migration
    expires: 2026-10-10
  - id: EXC-GROUP
    rules: [MANDATORY_GROUP_DENIED]
    mandatory: true
    subjects: {groups: [auditors]}
    scope: {projects: [audit/reports]}
    reason: auditors may push reports although blocked
    expires: 2026-10-10
`))
	many := func(user string, n int) Request {
		r := push(user, "a/b", "refs/heads/x", fresh())
		r.Updates = nil
		for i := 0; i < n; i++ {
			r.Updates = append(r.Updates, RefUpdate{Old: zero, New: oid, Ref: fmt.Sprintf("refs/tags/v%d", i)})
		}
		return r
	}
	cases := []struct {
		name   string
		req    Request
		want   string
		waived string
	}{
		{"empty GL_USERNAME is @unknown and mandatory-denied", push("", "a/b", "refs/heads/x", fresh()), policy.MandatoryUserDenied, ""},
		{"mandatory beats scoped allow", push("gone", "a/b", "refs/heads/x", fresh()), policy.MandatoryUserDenied, ""},
		{"exception waives scoped deny on matching ref", push("alex", "a/b", "refs/heads/hotfix/42", fresh()), "", "EXC-ALEX-HOTFIX"},
		{"exception does not match other ref", push("alex", "a/b", "refs/heads/main", fresh()), policy.UserPushDenied, ""},
		{"expired exception is ignored", push("alex", "a/old", "refs/heads/x", fresh()), policy.UserPushDenied, ""},
		{"limit exceeded", many("bob", 3), policy.LimitRefUpdates, ""},
		{"limit waived for migration user", many("svc-migration", 3), "", "EXC-BULK"},
		{"mandatory group deny", push("mallory", "x/y", "refs/heads/x", fresh("blocked")), policy.MandatoryGroupDenied, ""},
		{"mandatory group deny waived via group exception", push("ann", "audit/reports", "refs/heads/x", fresh("blocked", "auditors")), "", "EXC-GROUP"},
		{"group exception ignored when cache expired", push("ann", "audit/reports", "refs/heads/x", expired("blocked", "auditors")), policy.MandatoryGroupDenied, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := e.Evaluate(c.req)
			if got := codes(d); got != c.want {
				t.Fatalf("violations %q, want %q (waived %+v)", got, c.want, d.Waived)
			}
			var w []string
			for _, x := range d.Waived {
				w = append(w, x.ExceptionID)
			}
			if got := strings.Join(w, ","); got != c.waived {
				t.Fatalf("waived %q, want %q", got, c.waived)
			}
		})
	}
}

func TestMembershipUnavailable(t *testing.T) {
	src := `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
settings: {membership: {on_unavailable: %s}}
groups:
  contractors: {push: deny}
`
	none := Membership{State: "unavailable"}
	deny := New(compile(t, fmt.Sprintf(src, "deny_if_group_rules")))
	if got := codes(deny.Evaluate(push("bob", "a/b", "refs/heads/x", none))); got != policy.MembershipUnavailable {
		t.Fatalf("deny_if_group_rules: got %q", got)
	}
	if !deny.NeedsMembership() {
		t.Fatal("NeedsMembership should be true")
	}
	ignore := New(compile(t, fmt.Sprintf(src, "ignore_groups")))
	if got := codes(ignore.Evaluate(push("bob", "a/b", "refs/heads/x", none))); got != "" {
		t.Fatalf("ignore_groups: got %q", got)
	}
	// A policy without group denies never needs the cache.
	noGroups := New(compile(t, "apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: t, revision: 1}\nusers: {alex: {push: deny}}\n"))
	if noGroups.NeedsMembership() || codes(noGroups.Evaluate(push("bob", "a/b", "refs/heads/x", none))) != "" {
		t.Fatal("no group rules: cache must not matter")
	}
}

func TestRepositoryTypes(t *testing.T) {
	e := New(compile(t, `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
mandatory: {deny_users: [gone]}
settings: {repository_types: {design: none, wiki: mandatory_only}}
users:
  alex: {push: deny}
`))
	cases := []struct {
		repo, user, want string
	}{
		{"project-7", "alex", policy.UserPushDenied},
		{"wiki-7", "alex", ""},                         // mandatory_only: scoped identity skipped
		{"wiki-7", "gone", policy.MandatoryUserDenied}, // ...but mandatory applies
		{"design-7", "gone", ""},                       // none: not evaluated
		{"snippet-3", "gone", policy.MandatoryUserDenied},
		{"something-new-9", "alex", ""}, // unknown type: mandatory only
		{"something-new-9", "gone", policy.MandatoryUserDenied},
	}
	for _, c := range cases {
		r := push(c.user, "a/b", "refs/heads/x", fresh())
		r.Repository = c.repo
		if got := codes(e.Evaluate(r)); got != c.want {
			t.Errorf("%s/%s: got %q, want %q", c.repo, c.user, got, c.want)
		}
	}
	if NormalizeProject("Group/App.wiki", "wiki") != "group/app" {
		t.Error("wiki project path not normalised")
	}
}

// Worked examples of §7 about the effective content rules (enforced in 7-8).
func TestWorkedExamplesContent(t *testing.T) {
	e := example(t)
	mib := int64(1 << 20)
	t.Run("E08/E09 sizes", func(t *testing.T) {
		if c := e.ResolveContent("finance/payment-api", "refs/heads/main", "all"); c.MaxFileSize != 5*mib {
			t.Errorf("payment-api size %d (%s)", c.MaxFileSize, c.SizeSource)
		}
		if c := e.ResolveContent("finance/accounting", "refs/heads/main", "all"); c.MaxFileSize != 10*mib {
			t.Errorf("accounting size %d", c.MaxFileSize)
		}
		if c := e.ResolveContent("other/app", "refs/heads/main", "all"); c.MaxFileSize != 20*mib {
			t.Errorf("default size %d", c.MaxFileSize)
		}
	})
	t.Run("E10 project unblocks zip", func(t *testing.T) {
		if _, ok := e.ResolveContent("finance/reporting", "refs/heads/main", "all").Extensions["zip"]; ok {
			t.Error("zip should be unblocked for finance/reporting")
		}
		if _, ok := e.ResolveContent("finance/accounting", "refs/heads/main", "all").Extensions["zip"]; !ok {
			t.Error("zip should stay blocked elsewhere")
		}
	})
	t.Run("E11 audit mode in finance/legacy", func(t *testing.T) {
		c := e.ResolveContent("finance/legacy/billing", "refs/heads/main", "all")
		if c.Mode != "audit" || c.MandatoryMode != "enforce" {
			t.Errorf("mode %s / mandatory %s", c.Mode, c.MandatoryMode)
		}
		if c := e.ResolveContent("finance/legacy-erp", "refs/heads/main", "all"); c.Mode != "enforce" {
			t.Error("finance/legacy-erp is not under finance/legacy")
		}
	})
	t.Run("E15/E16 sandbox disabled: mandatory only", func(t *testing.T) {
		c := e.ResolveContent("sandbox/playground", "refs/heads/main", "all")
		if c.Enabled || len(c.Extensions) != 2 || !c.Extensions["exe"].Mandatory || c.MaxFileSize != 50*mib {
			t.Errorf("%+v", c)
		}
	})
	t.Run("E17/E18 PE signature on release branches only", func(t *testing.T) {
		if _, ok := e.ResolveContent("finance/accounting", "refs/heads/release/2.0", "all").Signatures["pe"]; !ok {
			t.Error("pe should apply on release/**")
		}
		if _, ok := e.ResolveContent("finance/accounting", "refs/heads/feature/x", "all").Signatures["pe"]; ok {
			t.Error("pe should not apply on feature branches")
		}
	})
	t.Run("mandatory cannot be removed", func(t *testing.T) {
		c := e.ResolveContent("finance/reporting", "refs/heads/main", "all")
		if !c.Extensions["dll"].Mandatory {
			t.Error("dll must be mandatory everywhere")
		}
	})
}

func TestContentMergeRules(t *testing.T) {
	e := New(compile(t, `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
mandatory: {max_file_size: 50MiB}
defaults:
  blocked_extensions: [zip]
  refs:
    "refs/heads/release/**": {max_file_size: 30MiB, blocked_extensions: [pdb]}
    "refs/heads/**": {max_file_size: 40MiB}
namespaces:
  a:
    refs:
      "refs/heads/release/**": {unblock_extensions: [zip, pdb], blocked_extensions: [zip], mode: audit}
`))
	c := e.ResolveContent("a/p", "refs/heads/release/1", "all")
	if c.MaxFileSize != 30<<20 {
		t.Errorf("smallest matching ref size should win: %d (%s)", c.MaxFileSize, c.SizeSource)
	}
	if _, ok := c.Extensions["zip"]; !ok {
		t.Error("same-layer block must win over unblock")
	}
	if _, ok := c.Extensions["pdb"]; ok {
		t.Error("pdb unblocked by the more specific namespace ref layer")
	}
	if c.Mode != "audit" {
		t.Errorf("mode %s", c.Mode)
	}
	if c := e.ResolveContent("b/p", "refs/tags/v1", "all"); c.MaxFileSize != 50<<20 {
		t.Errorf("no scoped size -> mandatory cap: %d", c.MaxFileSize)
	}
}
