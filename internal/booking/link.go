package booking

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Links signs and checks the manage-booking capability in decision #19.
//
// A booking code is an identifier, not an authenticator: six characters over a
// 32-letter alphabet, printed on paperwork and read out over the phone. It is
// deliberately guessable-adjacent, because a guest has to be able to say it. So
// anything that shows a guest's details or spends their money asks for this as
// well.
//
// Signed rather than stored. The alternative — a random token in a column — buys
// revocation this system has nowhere to offer yet, and costs a migration, a
// write on every booking, and a capability that does not exist for any stay
// booked before the column did. An HMAC over the code and an expiry is stateless,
// works for every booking there has ever been, and expires on its own.
type Links struct {
	secret []byte
}

// NewLinks returns nil for an empty secret.
//
// A nil *Links signs nothing and accepts nothing, which is what an unconfigured
// deploy gets: confirmation emails carry no manage link and the endpoints behind
// it refuse. Degraded rather than open — the failure mode of guessing at a
// secret would be a capability anybody could mint.
func NewLinks(secret string) *Links {
	if secret == "" {
		return nil
	}
	return &Links{secret: []byte(secret)}
}

// tokenParts is expiry and signature, joined by a dot. Both are URL-safe, so the
// whole token survives being pasted, wrapped by a mail client, and clicked.
const tokenSeparator = "."

// Sign issues a token for one booking, good until expires.
//
// The expiry is inside the signed message rather than beside it, so it cannot be
// pushed out by editing the URL.
func (l *Links) Sign(code string, expires time.Time) string {
	if l == nil {
		return ""
	}

	exp := strconv.FormatInt(expires.Unix(), 36)
	return exp + tokenSeparator + l.mac(code, exp)
}

// URL is the address that token belongs to.
//
// Absolute, because its only destination is an email. An empty siteURL — or an
// unconfigured signer — returns empty, and the template then omits the link
// rather than printing one that goes nowhere.
func (l *Links) URL(siteURL, code string, expires time.Time) string {
	token := l.Sign(code, expires)
	if token == "" || siteURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/booking/%s?t=%s", strings.TrimRight(siteURL, "/"), code, token)
}

// Valid reports whether a token authorises this booking at this moment.
//
// The comparison is constant time. It is checking a secret, and the timing of a
// byte-by-byte comparison is enough to recover one a byte at a time.
func (l *Links) Valid(code, token string, now time.Time) bool {
	if l == nil {
		return false
	}

	exp, mac, ok := strings.Cut(token, tokenSeparator)
	if !ok {
		return false
	}

	// The signature is checked before the expiry is trusted for anything, but
	// the expiry has to parse first, because it is part of the signed message.
	seconds, err := strconv.ParseInt(exp, 36, 64)
	if err != nil {
		return false
	}

	if !hmac.Equal([]byte(mac), []byte(l.mac(code, exp))) {
		return false
	}
	return now.Before(time.Unix(seconds, 0))
}

// mac signs the code and the expiry together.
//
// The separator is a NUL rather than nothing, so no pair of (code, expiry)
// values can be rearranged into another pair with the same signature.
func (l *Links) mac(code, exp string) string {
	h := hmac.New(sha256.New, l.secret)
	h.Write([]byte(strings.ToUpper(strings.TrimSpace(code))))
	h.Write([]byte{0})
	h.Write([]byte(exp))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
