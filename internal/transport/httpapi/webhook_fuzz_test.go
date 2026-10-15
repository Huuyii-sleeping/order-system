package httpapi

import (
	"testing"
	"time"
)

// FuzzWebhookVerificationNeverPanics explores arbitrary provider headers and
// payloads. Signature verification must fail closed; malformed input must not
// panic or be treated as a valid webhook.
func FuzzWebhookVerificationNeverPanics(f *testing.F) {
	for _, seed := range [][3]string{
		{"0", "", "{}"},
		{time.Now().Format("150405"), "00", `{"event_id":"event-1"}`},
		{"not-a-timestamp", "not-a-signature", "arbitrary payload"},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}

	handler := newNotificationWebhookHandler(
		nil,
		NotificationWebhookSecurity{Secret: "fuzz-only-secret", Tolerance: time.Minute},
		nil,
	)

	f.Fuzz(func(t *testing.T, timestamp, signature, payload string) {
		// Appending a non-hex byte guarantees this fuzz input is not a valid
		// signature while still exploring arbitrary timestamp/signature/payload
		// combinations through the parser and constant-time comparison path.
		if err := handler.verify(timestamp, signature+"!", []byte(payload)); err == nil {
			t.Fatal("malformed signature unexpectedly verified")
		}
	})
}
