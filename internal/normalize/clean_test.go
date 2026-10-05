package normalize

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestS18a_NormalizeStripsProcessorAndStoreNumbers(t *testing.T) {
	for raw, want := range map[string]struct{ name, processor string }{
		"SQ *BLUE BOTTLE COFFEE":                             {"Blue Bottle Coffee", "square"},
		"TST* TORCHYS TACOS #12 DALLAS TX":                   {"Torchy's Tacos", "toast"},
		"PAYPAL *SPOTIFY 402-935-7733":                       {"Spotify", "paypal"},
		"PURCHASE AUTHORIZED ON 09/12 HEB #423 ARLINGTON TX": {"H-E-B", ""},
		"CHEVRON 0097821 XXXX1234 POS DEB TERM 4412":         {"Chevron", ""},
		"Master Money Openai 3rd Street San Francisco Ca Eu": {"OpenAI", "mastercard"},
		"AMZN Mktp US*2K4":                                   {"Amazon", ""},
	} {
		got := Clean(raw)
		if got.Name != want.name || got.Processor != want.processor {
			t.Errorf("Clean(%q) = %q / %q, want %q / %q", raw, got.Name, got.Processor, want.name, want.processor)
		}
		if got.Key == "" || strings.ContainsAny(got.Key, "0123456789#*") {
			t.Errorf("the key carries no numbers or symbols: %q", got.Key)
		}
	}
	if got := Clean("TST* TORCHYS TACOS #12 DALLAS TX"); got.StoreNo != "12" {
		t.Errorf("the store number is kept as a feature: %q", got.StoreNo)
	}
	// different stores of one chain share a key
	a, b := Clean("TORCHYS TACOS #12 DALLAS TX"), Clean("TST* TORCHYS TACOS #98 AUSTIN TX")
	if a.Key != b.Key {
		t.Errorf("same merchant, different stores: %q vs %q", a.Key, b.Key)
	}
}

func TestS18a_BankStringsAreUntrusted(t *testing.T) {
	nasty := "COFFEE\x00SHOP‮\x07 " + strings.Repeat("A", 5000) + " $(rm -rf /) (.*)+ [unclosed"
	c := Clean(nasty)
	if len(c.Raw) > MaxDescriptionLen || !utf8.ValidString(c.Name) || strings.ContainsAny(c.Raw, "\x00\x07‮") {
		t.Errorf("sanitized: len %d valid %v %q", len(c.Raw), utf8.ValidString(c.Name), c.Raw[:20])
	}
	if got := Sanitize("a\xff\xfeb"); !utf8.ValidString(got) {
		t.Error("invalid UTF-8 is repaired")
	}
	if Clean("").Key != "" || Clean("   ").Name != "" {
		t.Error("empty input is fine")
	}
}

func TestS18a_AliasClusteringProposesNotApplies(t *testing.T) {
	names := []string{"Blue Bottle Coffee", "Blue Bottle Cofee", "BLUE BOTTLE COFFEE CO", "Chevron", "Kroger", "Kroger Fuel"}
	got := ClusterAliases(names, 0.6)
	want := [][]string{{"blue bottle cofee", "blue bottle coffee", "blue bottle coffee co"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if again := ClusterAliases(names, 0.6); !reflect.DeepEqual(got, again) {
		t.Error("deterministic")
	}
	if len(ClusterAliases([]string{"chevron", "shell"}, 0.6)) != 0 {
		t.Error("unrelated names are not proposed")
	}
	if s := Similarity("kroger", "kroger"); s != 1 {
		t.Errorf("identical: %v", s)
	}
}

func FuzzClean(f *testing.F) {
	for _, s := range []string{"SQ *BLUE BOTTLE", "TST* TACOS #12 TX", "\x00‮", "((((", "9999999999 12/31 XXXX1234"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c := Clean(s)
		if len(c.Raw) > MaxDescriptionLen || !utf8.ValidString(c.Name) || !utf8.ValidString(c.Key) {
			t.Fatalf("bad output for %q", s)
		}
	})
}
