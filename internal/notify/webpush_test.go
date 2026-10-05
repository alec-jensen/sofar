package notify

import "testing"

func TestS17_PushEndpointsDoNotAllowPrivateNetwork(t *testing.T) {
	for _, endpoint := range []string{"http://fcm.googleapis.com/a", "https://localhost/a", "https://127.0.0.1/a", "https://fcm.googleapis.com.evil.test/a", "https://user@fcm.googleapis.com/a", "https://fcm.googleapis.com:8080/a"} {
		if AllowedEndpoint(endpoint) {
			t.Errorf("unsafe endpoint accepted: %s", endpoint)
		}
	}
	for _, endpoint := range []string{"https://fcm.googleapis.com/fcm/send/abc", "https://updates.push.services.mozilla.com/wpush/v2/abc", "https://web.push.apple.com/abc"} {
		if !AllowedEndpoint(endpoint) {
			t.Errorf("valid provider rejected: %s", endpoint)
		}
	}
}
