package budget

import "testing"

func TestDirectoryPatternsAreNormalized(t *testing.T) {
	ids := map[string]bool{}
	for _, c := range Directory.Categories {
		ids[c.ID] = true
	}
	check := func(kind string, patterns []string) {
		for _, p := range patterns {
			if Normalize(p) != p || p == "" {
				t.Errorf("%s pattern %q should be written as %q", kind, p, Normalize(p))
			}
		}
	}
	for _, m := range append(Directory.Merchants, Directory.Keywords...) {
		if !ids[m.Sub] {
			t.Errorf("%v points to unknown category %q", m.Match, m.Sub)
		}
		check("merchant", m.Match)
	}
	for _, e := range Directory.Income {
		check("income", e.Match)
	}
	check("transfer", Directory.Transfers)
	check("card", Directory.CardPayments)
}

func TestPrettifyCleansBankDescriptions(t *testing.T) {
	for raw, want := range map[string]string{
		"Master Money Openai 3rd Street San Francisco Ca Eu":              "OpenAI",
		"Master Money Ajs Hot Chicken W Campbell Ste Richardson Tx Djcda": "Ajs Hot Chicken",
		"Martin Hs Theatre 6319 Settlement Dr Arlington Tx":               "Martin Hs Theatre",
		"Master Money Kwik Market N Little":                               "Kwik Market",
		"Micro Electronics Central Expres":                                "Micro Center",
		"SQ *BLUE BOTTLE COFFEE":                                          "Blue Bottle Coffee",
		"TST* TORCHYS TACOS #12 DALLAS TX":                                "Torchy's Tacos",
		"AMZN Mktp US*2K4":                                                "Amazon",
		"PURCHASE AUTHORIZED ON 09/12 HEB #423 ARLINGTON TX":              "H-E-B",
		"Google Fi Wireless":                                              "Google Fi",
		"7-Eleven":                                                        "7-Eleven",
		"Transfer to Share 30":                                            "Transfer to Share",
		"Point of Rental":                                                 "Point of Rental",
	} {
		if got := Prettify(raw); got != want {
			t.Errorf("Prettify(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestLookupSortsCommonMerchants(t *testing.T) {
	for name, want := range map[string]string{
		"McDonald's": "dining", "Whataburger": "dining", "Handel's Ice Cream": "coffee", "Shell": "gas",
		"QuikTrip": "gas", "AutoZone": "transport", "Amazon": "shopping", "Google Fi": "phone",
		"Grubhub": "dining", "Ajs Hot Chicken": "dining", "Martin Hs Theatre": "entertainment",
		"Kwik Market": "gas", "Fidelity Brokerage Services": "investing", "Joe's Coffee Shop": "coffee",
		"Round Rock Donuts": "dining", "Uber Eats": "dining", "Uber": "transport",
	} {
		if sub, _, _ := LookupMerchant(name); sub != want {
			t.Errorf("LookupMerchant(%q) = %q, want %q", name, sub, want)
		}
	}
	for name, want := range map[string]string{
		"ACME CORP PAYROLL": "salary", "Dividend": "other", "Interest Paid": "other",
		"Transfer from Share 30": "transfer", "Fidelity Brokerage Services": "transfer", "Point of Rental": "",
		"Transfer from JENSEN,CODY R": "",
	} {
		if got := LookupIncome(name); got != want {
			t.Errorf("LookupIncome(%q) = %q, want %q", name, got, want)
		}
	}
}
