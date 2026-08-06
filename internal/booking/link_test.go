package booking

import (
	"strings"
	"testing"
	"time"
)

const testSecret = "a-long-random-signing-secret"

func TestASignedLinkOpensItsOwnBookingAndNothingElse(t *testing.T) {
	links := NewLinks(testSecret)
	now := time.Now()

	token := links.Sign("BH-ABC234", now.Add(time.Hour))

	if !links.Valid("BH-ABC234", token, now) {
		t.Fatal("a freshly signed token was rejected")
	}

	// The whole point. A guest holding one link must not be able to open, or
	// cancel, somebody else's stay by editing the code in the address bar.
	if links.Valid("BH-XYZ789", token, now) {
		t.Error("a token signed for one booking authorised another")
	}
}

// Codes are stored and compared uppercase, so a guest whose mail client
// lowercased the URL is not locked out of their own booking.
func TestCaseAndSpaceInTheCodeDoNotChangeTheSignature(t *testing.T) {
	links := NewLinks(testSecret)
	now := time.Now()

	token := links.Sign("BH-ABC234", now.Add(time.Hour))
	if !links.Valid(" bh-abc234 ", token, now) {
		t.Error("a lowercased code was treated as a different booking")
	}
}

func TestAnExpiredLinkIsRefused(t *testing.T) {
	links := NewLinks(testSecret)
	now := time.Now()

	token := links.Sign("BH-ABC234", now.Add(time.Hour))

	if links.Valid("BH-ABC234", token, now.Add(2*time.Hour)) {
		t.Error("a token was accepted after its expiry")
	}
}

// The expiry is inside the signed message. Pushing it out has to break the
// signature, or the expiry is a suggestion.
func TestTheExpiryCannotBeMovedByEditingTheURL(t *testing.T) {
	links := NewLinks(testSecret)
	now := time.Now()

	token := links.Sign("BH-ABC234", now.Add(time.Hour))
	_, mac, _ := strings.Cut(token, tokenSeparator)

	forged := links.Sign("BH-ABC234", now.Add(100*24*time.Hour))
	stretched, _, _ := strings.Cut(forged, tokenSeparator)

	if links.Valid("BH-ABC234", stretched+tokenSeparator+mac, now.Add(2*time.Hour)) {
		t.Error("an expiry swapped into a valid signature was accepted")
	}
}

func TestATokenFromAnotherSecretIsRefused(t *testing.T) {
	mine := NewLinks(testSecret)
	theirs := NewLinks("some-other-deployments-secret")
	now := time.Now()

	if mine.Valid("BH-ABC234", theirs.Sign("BH-ABC234", now.Add(time.Hour)), now) {
		t.Error("a token signed with a different secret was accepted")
	}
}

func TestRubbishIsRefusedRatherThanPanicking(t *testing.T) {
	links := NewLinks(testSecret)
	now := time.Now()

	for _, token := range []string{"", ".", "abc", "zzz.zzz", "....", strings.Repeat("a", 500)} {
		if links.Valid("BH-ABC234", token, now) {
			t.Errorf("accepted %q as a token", token)
		}
	}
}

// No secret configured means no capability exists — not one that everybody
// holds. The failure has to be closed.
func TestAnUnconfiguredSignerSignsNothingAndAcceptsNothing(t *testing.T) {
	links := NewLinks("")
	if links != nil {
		t.Fatal("an empty secret produced a signer")
	}

	if got := links.Sign("BH-ABC234", time.Now().Add(time.Hour)); got != "" {
		t.Errorf("a nil signer minted %q", got)
	}
	if got := links.URL("https://bealhouse.com", "BH-ABC234", time.Now().Add(time.Hour)); got != "" {
		t.Errorf("a nil signer built %q", got)
	}
	if links.Valid("BH-ABC234", "anything", time.Now()) {
		t.Error("a nil signer accepted a token")
	}
}

// Mail cannot carry a relative path, so a URL with no origin is no URL.
func TestURLIsAbsoluteOrAbsent(t *testing.T) {
	links := NewLinks(testSecret)
	expires := time.Now().Add(time.Hour)

	got := links.URL("https://bealhouse.com/", "BH-ABC234", expires)
	if !strings.HasPrefix(got, "https://bealhouse.com/booking/BH-ABC234?t=") {
		t.Errorf("URL = %q", got)
	}
	if strings.Contains(got, "com//booking") {
		t.Errorf("a trailing slash on the origin doubled up: %q", got)
	}

	if got := links.URL("", "BH-ABC234", expires); got != "" {
		t.Errorf("built %q with no origin to be absolute against", got)
	}
}
