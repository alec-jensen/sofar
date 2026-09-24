package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPushEndpointsDoNotAllowPrivateNetwork(t *testing.T) {
	for _, endpoint := range []string{"http://fcm.googleapis.com/a", "https://localhost/a", "https://127.0.0.1/a", "https://fcm.googleapis.com.evil.test/a", "https://user@fcm.googleapis.com/a", "https://fcm.googleapis.com:8080/a"} {
		if allowedPushEndpoint(endpoint) {
			t.Errorf("unsafe endpoint accepted: %s", endpoint)
		}
	}
	for _, endpoint := range []string{"https://fcm.googleapis.com/fcm/send/abc", "https://updates.push.services.mozilla.com/wpush/v2/abc", "https://web.push.apple.com/abc"} {
		if !allowedPushEndpoint(endpoint) {
			t.Errorf("valid provider rejected: %s", endpoint)
		}
	}
}
func TestCSRFAndHTTPSRejectBeforeHandler(t *testing.T) {
	s := &server{origin: "https://money.example.com", production: true}
	for _, tc := range []struct {
		origin     string
		tls, proxy bool
		want       int
	}{{"https://evil.test", true, false, 403}, {"https://money.example.com", false, false, 400}, {"", true, false, 403}} {
		r := httptest.NewRequest("POST", "http://money.example.com/api/actions", strings.NewReader("{}"))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		if tc.tls {
			r = httptest.NewRequest("POST", "https://money.example.com/api/actions", strings.NewReader("{}"))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("got %d want %d", w.Code, tc.want)
		}
	}
}
func TestDecoderRejectsTrailingAndUnknownFields(t *testing.T) {
	for _, body := range []string{`{"Code":"123456"} {}`, `{"Code":"123456","unexpected":true}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var in struct{ Code string }
		if decode(r, &in) == nil {
			t.Errorf("accepted invalid body %s", body)
		}
	}
}
