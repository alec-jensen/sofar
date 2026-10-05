package normalize

// stableIDs maps the directory's legacy category ids to the stable S1a ids
// (docs/plan/legacy-migration.md). M6 re-maps the directory itself.
var stableIDs = map[string]string{
	"housing": "rent_mortgage", "utilities": "utilities", "phone": "internet_phone", "insurance": "insurance",
	"subscriptions": "other_subscriptions", "loans": "loan_payments", "education": "education",
	"groceries": "groceries", "dining": "dining_out", "coffee": "dining_out", "gas": "fuel", "transport": "auto_transit",
	"shopping": "shopping", "home": "home_household", "entertainment": "entertainment", "health": "health_medical",
	"personal-care": "personal_care", "fitness": "memberships", "travel": "travel", "gifts": "gifts_donations",
	"pets": "other_spending", "fees": "fees_interest", "savings-account": "general_savings", "investing": "investments",
}

// StableCategory returns the S1a category id for a directory category id, or "".
func StableCategory(sub string) string { return stableIDs[sub] }

// MerchantKey is the normalized merchant name used to group recurring charges:
// processor prefixes, store numbers and locations are dropped first.
func MerchantKey(description string) string { return Normalize(Prettify(description)) }
