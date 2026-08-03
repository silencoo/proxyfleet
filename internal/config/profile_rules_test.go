package config

import (
	"strings"
	"testing"
)

func TestProfileNameMatcherComposesRuleGroupsAndLegacyRegex(t *testing.T) {
	matcher, err := CompileProfileNameMatcher(ProfileConfig{
		Name: "premium", NameRegex: `(?i)premium`,
		TagRules: ProfileTagRules{
			Any:  []string{`(?i)hong kong|\bhk\b`, `(?i)japan|\bjp\b`},
			Must: []string{`(?i)dedicated`}, MustNot: []string{`(?i)expired|traffic left`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if match := matcher.Match("HK Premium Dedicated 01"); !match.Allowed {
		t.Fatalf("matching name was rejected: %#v", match)
	}
	for _, test := range []struct{ name, group string }{
		{"US Premium Dedicated 01", "any"},
		{"HK Dedicated 01", "must"},
		{"JP Premium Shared 01", "must"},
		{"JP Premium Dedicated Expired", "must_not"},
	} {
		if match := matcher.Match(test.name); match.Allowed || match.Group != test.group {
			t.Fatalf("Match(%q)=%#v, want rejection by %s", test.name, match, test.group)
		}
	}
}

func TestNormalizeProfileRulesDeduplicatesAndReportsIndexedRegex(t *testing.T) {
	values, err := normalizeProfileRules("hk", "any", []string{" HK ", "HK", "JP"})
	if err != nil || len(values) != 2 || values[0] != "HK" {
		t.Fatalf("normalized=%v err=%v", values, err)
	}
	_, err = normalizeProfileRules("hk", "must_not", []string{"ok", "["})
	if err == nil || !strings.Contains(err.Error(), "must_not[1]") {
		t.Fatalf("invalid regex error=%v", err)
	}
}
