package ledger

import "github.com/alec-jensen/sofar/internal/events"

// Overlay event kinds. The subject is always ("txn", <raw transaction id>).
const (
	KindSetCategory          = "overlay.set_category"
	KindSetNote              = "overlay.set_note"
	KindSetTags              = "overlay.set_tags"
	KindHide                 = "overlay.hide"
	KindUnhide               = "overlay.unhide"
	KindSetMonth             = "overlay.set_month"
	KindSplit                = "overlay.split"
	KindSetFromDiscretionary = "overlay.set_from_discretionary"
	KindSetReviewed          = "overlay.set_reviewed"
	KindSetType              = "overlay.set_type"

	// KindCategoryMerge and KindCategoryDelete remap every use of one category to another.
	KindCategoryMerge  = "category.merge"
	KindCategoryDelete = "category.delete"
	SubjectCategory    = "category"

	SubjectTxn = "txn"
)

// SetCategory assigns (or with nil clears) a category.
type SetCategory struct {
	TxnID      string  `json:"txn_id"`
	CategoryID *string `json:"category_id"`
}

// SetNote sets the free-text note.
type SetNote struct {
	TxnID string `json:"txn_id"`
	Note  string `json:"note"`
}

// SetTags replaces the tags.
type SetTags struct {
	TxnID string   `json:"txn_id"`
	Tags  []string `json:"tags"`
}

// Hide excludes the transaction from all math (S11) and tombstones it so sync
// never re-imports it.
type Hide struct {
	TxnID  string `json:"txn_id"`
	Reason string `json:"reason,omitempty"`
}

// Unhide reverses Hide.
type Unhide struct {
	TxnID string `json:"txn_id"`
}

// SetMonth reassigns the transaction to a month ("2026-10"), or with nil clears the override (S3).
type SetMonth struct {
	TxnID string  `json:"txn_id"`
	Month *string `json:"month"`
}

// SplitPart is one part of a split transaction. Amounts keep the transaction's sign.
type SplitPart struct {
	Amount     int64    `json:"amount"`
	CategoryID string   `json:"category_id"`
	Tags       []string `json:"tags,omitempty"`
	Note       string   `json:"note,omitempty"`
}

// Split divides a transaction into parts that must sum to its total (S19.6).
// An empty Parts list removes the split.
type Split struct {
	TxnID string      `json:"txn_id"`
	Parts []SplitPart `json:"parts"`
}

// SetFromDiscretionary marks a purchase as drawn from discretionary (S9).
type SetFromDiscretionary struct {
	TxnID string `json:"txn_id"`
	Value bool   `json:"value"`
}

// SetReviewed marks the transaction reviewed (or not). The event time is the review time.
type SetReviewed struct {
	TxnID    string `json:"txn_id"`
	Reviewed bool   `json:"reviewed"`
}

// SetType sets the transaction type override ("income", "transfer", ...), or "" to clear.
type SetType struct {
	TxnID string `json:"txn_id"`
	Type  string `json:"type"`
}

// CategoryRemap says every transaction and split using From now uses To. Both
// the merge and the delete events carry it; the overlay projection applies it.
type CategoryRemap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Register adds the overlay payloads to an event registry.
func Register(r *events.Registry) {
	r.Register(KindSetCategory, 1, SetCategory{})
	r.Register(KindSetNote, 1, SetNote{})
	r.Register(KindSetTags, 1, SetTags{})
	r.Register(KindHide, 1, Hide{})
	r.Register(KindUnhide, 1, Unhide{})
	r.Register(KindSetMonth, 1, SetMonth{})
	r.Register(KindSplit, 1, Split{})
	r.Register(KindSetFromDiscretionary, 1, SetFromDiscretionary{})
	r.Register(KindSetReviewed, 1, SetReviewed{})
	r.Register(KindSetType, 1, SetType{})
	r.Register(KindCategoryMerge, 1, CategoryRemap{})
	r.Register(KindCategoryDelete, 1, CategoryRemap{})
}
