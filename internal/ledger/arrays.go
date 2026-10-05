package ledger

import "github.com/alec-jensen/sofar/internal/store"

// textArray is used with an explicit cast such as `$1::uuid[]`.
func textArray(a []string) store.TextArray { return store.TextArray(a) }
