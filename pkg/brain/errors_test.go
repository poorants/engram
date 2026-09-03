package brain

import (
	"fmt"
	"net/http"
	"testing"
)

// These two classifiers are what a non-model caller branches on, so getting
// them backwards is expensive in a specific way: treating an unreachable store
// as a refusal would divert the document into a local file nobody reads, and
// the team would believe it was recorded.
func TestRefusedAndUnreachableAreDifferentAnswers(t *testing.T) {
	refused := &APIError{Status: http.StatusForbidden, Message: "scope denied"}
	if !Refused(refused) {
		t.Fatal("403 must be Refused — that is the local-file-brain path")
	}
	if Unreachable(refused) {
		t.Fatal("403 must NOT be Unreachable — the store answered, it declined")
	}

	down := &APIError{Status: http.StatusBadGateway, Message: "bad gateway"}
	if Refused(down) {
		t.Fatal("5xx must not be read as a refusal — that would divert the write")
	}
	if !Unreachable(down) {
		t.Fatal("5xx must be Unreachable")
	}

	// A transport failure carries no status at all and is still unreachable.
	transport := &TransportError{BaseURL: "http://x", Err: fmt.Errorf("connection refused")}
	if !Unreachable(transport) {
		t.Fatal("a transport error must be Unreachable")
	}
	if Refused(transport) {
		t.Fatal("a transport error is not a scope refusal")
	}

	// A validation error never reached the network. Calling it an outage sends
	// the caller to check their VPN instead of their arguments.
	if Unreachable(fmt.Errorf("note is empty")) {
		t.Fatal("a validation error must not be reported as an unreachable store")
	}

	// An unset store address is a setup error for the same reason.
	if Unreachable(ErrNoStore) || Refused(ErrNoStore) {
		t.Fatal("ErrNoStore is a configuration error, neither an outage nor a refusal")
	}

	if Refused(nil) || Unreachable(nil) {
		t.Fatal("nil is neither")
	}

	// 404 is a missing document, not a refusal and not an outage.
	missing := &APIError{Status: http.StatusNotFound}
	if Refused(missing) || Unreachable(missing) {
		t.Fatal("404 must be a plain error, not refused or unreachable")
	}
}

// There is deliberately no built-in store address, so an unconfigured client
// must say "you have not set one" rather than time out against a host that only
// exists on somebody else's network.
func TestUnconfiguredClientFailsWithSetupError(t *testing.T) {
	c := New(Config{})
	if c.Configured() {
		t.Fatal("a client with no BaseURL must not report itself configured")
	}
	if _, err := c.Search(t.Context(), SearchOpts{Query: "anything"}); err != ErrNoStore {
		t.Fatalf("Search error = %v, want ErrNoStore", err)
	}
	if _, err := c.PreparePut("acme/webapp/resources/x.md", "body", "note", "me"); err != ErrNoStore {
		t.Fatalf("PreparePut error = %v, want ErrNoStore", err)
	}
}
