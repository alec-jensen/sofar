package httpx

import (
	"net/http/httptest"
	"testing"
)

func TestPageParams(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?limit=500&cursor="+EncodeCursor("2026-10-05|42"), nil)
	pos, limit, err := PageParams(r, 50, 100)
	if err != nil || pos != "2026-10-05|42" || limit != 100 {
		t.Fatalf("got %q %d %v", pos, limit, err)
	}
	_, limit, err = PageParams(httptest.NewRequest("GET", "/x", nil), 50, 100)
	if err != nil || limit != 50 {
		t.Fatalf("defaults: %d %v", limit, err)
	}
	for _, q := range []string{"limit=0", "limit=abc", "cursor=!!!"} {
		if _, _, err = PageParams(httptest.NewRequest("GET", "/x?"+q, nil), 50, 100); err == nil {
			t.Errorf("%s should be rejected", q)
		}
	}
}
