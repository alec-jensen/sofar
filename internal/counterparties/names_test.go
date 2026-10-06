package counterparties

import "testing"

func TestParseName(t *testing.T) {
	cases := map[string]string{
		"VENMO PAYMENT FROM SMITH,JOHN Q":     "john smith",
		"Zelle payment from John Smith 12345": "john smith",
		"Transfer from Smith,John Q":          "john smith",
		"Transfer from DAD,":                  "dad",
		"ZELLE FROM JOHN S":                   "john/s",
		"CASH APP*J SMITH":                    "smith/j",
		"@dadj":                               "@dadj",
	}
	for in, want := range cases {
		if got := ParseName(in).Key(); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
	if !ParseName("ZELLE PAYMENT 99231").Empty() {
		t.Error("a payment with no person has no name")
	}
}

func TestSimilarity(t *testing.T) {
	smith := ParseName("John Smith")
	for in, want := range map[string]float64{
		"SMITH,JOHN Q": 1,
		"JOHN S":       0.7,
		"JOHN SMIT":    0.7,
		"JOHN STONE":   0,
		"JANE SMITH":   0,
		"J SMITH":      0.7,
	} {
		if got := Similarity(ParseName(in), smith); got != want {
			t.Errorf("%q: %v want %v", in, got, want)
		}
	}
}

func TestPlatformOf(t *testing.T) {
	for in, want := range map[string]Platform{"VENMO PAYMENT": PlatformVenmo, "Zelle from X": PlatformZelle, "Transfer from Smith": PlatformBank, "GROCERY": PlatformOther} {
		if PlatformOf(in) != want {
			t.Errorf("%q", in)
		}
	}
}
