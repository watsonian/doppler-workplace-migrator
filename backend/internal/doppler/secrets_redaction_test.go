package doppler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

// The secrets write is the one request whose body is secret material, so it is
// the one place a target can hand a value back to us. These cover the response
// coming back with the value in it, however it got there.
func TestRedactSubmittedValues(t *testing.T) {
	const value = "sk_live_51H8xQ2KZvNq7mJfR0pWdYc"

	changes := []SecretChangeRequest{
		{Name: "STRIPE_KEY", Value: ptr(value)},
		{Name: "EMPTY", Value: nil},
	}

	// The real message Doppler returns for a nested reference.
	err := redactSubmittedValues(&APIError{
		StatusCode: 400,
		Message:    "Invalid reference format: " + value,
		Body:       `{"messages":["Invalid reference format: ` + value + `"],"success":false}`,
	}, changes)

	apiErr := err.(*APIError)
	if strings.Contains(apiErr.Message, value) {
		t.Errorf("value survived in Message: %q", apiErr.Message)
	}
	if strings.Contains(apiErr.Body, value) {
		t.Errorf("value survived in Body: %q", apiErr.Body)
	}
	if !strings.Contains(apiErr.Message, redactedMarker) {
		t.Errorf("redaction is not signposted: %q", apiErr.Message)
	}
	// The rest of the message is what makes the failure diagnosable, so it stays.
	if !strings.Contains(apiErr.Message, "Invalid reference format") {
		t.Errorf("redaction ate the explanation: %q", apiErr.Message)
	}
}

func TestRedactSubmittedValuesLeavesShortValuesAlone(t *testing.T) {
	// A two-character value occurs by chance in ordinary prose; replacing it
	// everywhere would corrupt the message while protecting nothing worth having.
	err := redactSubmittedValues(&APIError{
		StatusCode: 400,
		Message:    "Invalid reference format: on",
	}, []SecretChangeRequest{{Name: "DEBUG", Value: ptr("on")}})

	if got := err.(*APIError).Message; !strings.Contains(got, "on") {
		t.Errorf("Message = %q", got)
	}
}

func TestRedactSubmittedValuesPassesThroughNonAPIErrors(t *testing.T) {
	if got := redactSubmittedValues(nil, nil); got != nil {
		t.Errorf("nil error became %v", got)
	}
}

// Error() is written to the checkpoint file, the logs, the event stream and the
// CSV. A response body is whatever the other end sent, and it does not belong in
// all four.
func TestErrorStringNeverCarriesTheResponseBody(t *testing.T) {
	body := `<html><body>Gateway timeout: upstream said ` + strings.Repeat("x", 4000) + `</body></html>`

	apiErr := &APIError{StatusCode: 502, Body: body}
	got := apiErr.Error()

	if strings.Contains(got, "<html>") || strings.Contains(got, "xxxx") {
		t.Errorf("Error() leaked the body: %q", got)
	}
	if !strings.Contains(got, "502") {
		t.Errorf("Error() dropped the status: %q", got)
	}
	// The body is not lost — it is published deliberately and in bounded form.
	if apiErr.Body != body {
		t.Error("Body was mutated; Evidence needs it intact")
	}

	// The API's own message is safe and is what makes a failure readable.
	withMessage := &APIError{StatusCode: 403, Message: "You do not have access", Body: body}
	if !strings.Contains(withMessage.Error(), "You do not have access") {
		t.Errorf("Error() dropped the message: %q", withMessage.Error())
	}
	if strings.Contains(withMessage.Error(), "<html>") {
		t.Errorf("Error() leaked the body alongside the message: %q", withMessage.Error())
	}
}

// The API always sends computed alongside raw. Not declaring it on the struct is
// what keeps a second copy of every secret value out of the process, so this
// pins that the resolved value is discarded at parse time rather than held.
func TestListSecretsDoesNotRetainResolvedValues(t *testing.T) {
	const resolved = "sk_live_51H8xQ2KZvNq7mJfR0pWdYc"

	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/secrets": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"secrets": map[string]any{
					"STRIPE_KEY": map[string]any{
						// A reference: raw is the expression, computed is the
						// secret it points at.
						"raw":                "${shared.prd.STRIPE_KEY}",
						"computed":           resolved,
						"rawVisibility":      "restricted",
						"computedVisibility": "restricted",
						"rawValueType":       map[string]string{"type": "string"},
						"computedValueType":  map[string]string{"type": "string"},
					},
				},
			})
		},
	})
	defer server.Close()

	secrets, err := client.ListSecrets(context.Background(), "web", "prd")
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	got := secrets["STRIPE_KEY"]
	// Raw is what gets written, so that the reference survives as a reference
	// rather than being flattened into the value it happened to resolve to.
	if got.Raw != "${shared.prd.STRIPE_KEY}" {
		t.Errorf("Raw = %q, want the unresolved reference", got.Raw)
	}
	if got.RawVisibility != "restricted" {
		t.Errorf("RawVisibility = %q", got.RawVisibility)
	}

	// Round-trip the parsed map: the resolved value must not be anywhere in it.
	encoded, err := json.Marshal(secrets)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), resolved) {
		t.Errorf("the resolved value was retained: %s", encoded)
	}
}
