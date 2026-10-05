package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRejectsTrailingAndUnknownFields(t *testing.T) {
	for _, body := range []string{`{"Code":"123456"} {}`, `{"Code":"123456","unexpected":true}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var in struct{ Code string }
		if Decode(r, &in) == nil {
			t.Errorf("accepted invalid body %s", body)
		}
	}
}

func TestFailUsesStandardShape(t *testing.T) {
	w := httptest.NewRecorder()
	Fail(w, 401, "unauthorized", "sign in first")
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"code":"unauthorized"`) || !strings.Contains(w.Body.String(), `"message":"sign in first"`) {
		t.Fatalf("unexpected body %d %s", w.Code, w.Body.String())
	}
}
