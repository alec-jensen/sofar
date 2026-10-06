package categorize

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/alec-jensen/sofar/internal/normalize"
	"github.com/alec-jensen/sofar/internal/rules"
)

// SeedPack names the seed rule pack; installing it again replaces only its own rules.
const SeedPack = "sofar-seed-v1"

func sp(s string) *string { return &s }

// phraseRegex matches any of the names as whole words in a normalized merchant key.
func phraseRegex(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	return "(^| )(" + strings.Join(quoted, "|") + ")( |$)"
}

// chunk splits names so each regex stays within the rule limit.
func chunk(names []string) [][]string {
	var out [][]string
	var cur []string
	size := 20
	for _, n := range names {
		if size+len(n)+3 > rules.MaxRegexLen-10 && len(cur) > 0 {
			out = append(out, cur)
			cur, size = nil, 20
		}
		cur = append(cur, n)
		size += len(n) + 3
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// SeedRules builds the built-in rules from the merchant directory, re-mapped to
// the stable S1a category ids. They sit in the lowest layer, so anything the
// user or the learner decides wins. Keyword rules rank below named merchants,
// and together they are also the starter prior (S18a).
func SeedRules() []rules.Rule {
	type group struct {
		category string
		names    []string
	}
	merchants := map[string][]string{}
	keywords := map[string][]string{}
	for _, m := range normalize.Directory.Merchants {
		cat := normalize.StableCategory(m.Sub)
		if cat == "" {
			continue
		}
		merchants[cat] = append(merchants[cat], m.Match...)
	}
	for _, k := range normalize.Directory.Keywords {
		cat := normalize.StableCategory(k.Sub)
		if cat == "" {
			continue
		}
		keywords[cat] = append(keywords[cat], k.Match...)
	}
	var out []rules.Rule
	emit := func(kind string, priority int, byCat map[string][]string) {
		cats := make([]string, 0, len(byCat))
		for c := range byCat {
			cats = append(cats, c)
		}
		sort.Strings(cats)
		for _, c := range cats {
			names := append([]string(nil), byCat[c]...)
			sort.Strings(names)
			for i, part := range chunk(names) {
				out = append(out, rules.Rule{
					Name: kind + " " + c + " " + string(rune('a'+i%26)), Layer: rules.LayerSeed, Stage: rules.StageDefault, Priority: priority, Enabled: true, Provenance: "seed",
					Conditions: rules.Condition{Op: "and", Args: []rules.Condition{
						{Field: "direction", Op: "is", Value: "out"},
						{Field: "merchant", Op: "regex", Value: phraseRegex(part)},
					}},
					Actions: rules.Actions{Category: sp(c)},
				})
			}
		}
	}
	emit("merchants", 100, merchants)
	emit("keywords", 200, keywords)

	// income streams by payer
	streams := map[string]string{"salary": "salary", "self-employed": "self_employment", "other": "gift_other"}
	incomeNames := map[string][]string{}
	for _, e := range normalize.Directory.Income {
		if cat := streams[e.Stream]; cat != "" {
			incomeNames[cat] = append(incomeNames[cat], e.Match...)
		}
	}
	cats := make([]string, 0, len(incomeNames))
	for c := range incomeNames {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		names := append([]string(nil), incomeNames[c]...)
		sort.Strings(names)
		for _, part := range chunk(names) {
			out = append(out, rules.Rule{Name: "income " + c, Layer: rules.LayerSeed, Stage: rules.StageDefault, Priority: 150, Enabled: true, Provenance: "seed",
				Conditions: rules.Condition{Op: "and", Args: []rules.Condition{{Field: "direction", Op: "is", Value: "in"}, {Field: "merchant", Op: "regex", Value: phraseRegex(part)}}},
				Actions:    rules.Actions{Category: sp(c)}})
		}
	}

	// transfers and card payments are neutral (S12, S13)
	for _, t := range []struct {
		name, cat string
		names     []string
	}{{"transfers", "transfer", normalize.Directory.Transfers}, {"card payments", "credit_card_payment", normalize.Directory.CardPayments}} {
		names := append([]string(nil), t.names...)
		sort.Strings(names)
		for _, part := range chunk(names) {
			out = append(out, rules.Rule{Name: t.name, Layer: rules.LayerSeed, Stage: rules.StagePre, Priority: 50, Enabled: true, Provenance: "seed",
				Conditions: rules.Condition{Field: "description", Op: "regex", Value: strings.ReplaceAll(phraseRegex(part), "(^| )", `(^|\s)`)},
				Actions:    rules.Actions{Category: sp(t.cat)}})
		}
	}

	// fees and card interest are spending (S11, S13)
	out = append(out, rules.Rule{Name: "bank fees and card interest", Layer: rules.LayerSeed, Stage: rules.StageDefault, Priority: 80, Enabled: true, Provenance: "seed",
		Conditions: rules.Condition{Op: "and", Args: []rules.Condition{
			{Field: "direction", Op: "is", Value: "out"},
			{Field: "description", Op: "regex", Value: `(interest charge|purchase interest|finance charge|overdraft|nsf fee|insufficient funds|monthly service fee|maintenance fee|atm fee|foreign transaction fee|late fee|annual fee|wire fee)`},
		}},
		Actions: rules.Actions{Category: sp("fees_interest")}})
	out = append(out, rules.Rule{Name: "interest earned", Layer: rules.LayerSeed, Stage: rules.StageDefault, Priority: 80, Enabled: true, Provenance: "seed",
		Conditions: rules.Condition{Op: "and", Args: []rules.Condition{
			{Field: "direction", Op: "is", Value: "in"},
			{Field: "description", Op: "regex", Value: `(interest paid|interest payment|interest earned|dividend|apy earned|interest credit)`},
		}},
		Actions: rules.Actions{Category: sp("interest")}})
	return out
}

// InstallSeeds installs the seed pack. It is idempotent: the pack's earlier
// rules are replaced and nothing else is touched, so user and learned rules
// are never overwritten.
func (s *Service) InstallSeeds(ctx context.Context, tx *sql.Tx) (int, error) {
	// Reuse the store's pack import so replacement semantics live in one place.
	var raw []map[string]any
	for _, r := range SeedRules() {
		var cond, act any
		cj, _ := json.Marshal(r.Conditions)
		aj, _ := json.Marshal(r.Actions)
		json.Unmarshal(cj, &cond)
		json.Unmarshal(aj, &act)
		raw = append(raw, map[string]any{"name": r.Name, "layer": "seed", "stage": string(r.Stage), "conditions": cond, "actions": act, "priority": r.Priority, "enabled": true, "provenance": "seed"})
	}
	data, _ := json.Marshal(map[string]any{"version": 1, "pack": SeedPack, "rules": raw})
	added, _, err := s.Rules.Import(ctx, tx, data)
	return added, err
}
