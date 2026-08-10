package main

import (
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// vapid prints a fresh VAPID key pair for PUSH_VAPID_PUBLIC_KEY and
// PUSH_VAPID_PRIVATE_KEY.
//
// A subcommand rather than an instruction to go and find a tool, for the same
// reason `enroll` and `migrate` are here: a deploy is one binary on a server
// with no repository beside it, and the thing that needs the keys is this
// program.
//
// **Generating a new pair invalidates every existing subscription.** A browser
// subscribes against one public key and the push service will not accept a
// delivery signed by anything else, so every phone has to turn notifications on
// again. That is why this prints and does not write: rotating the keys should
// take a deliberate edit to the environment, not a run of a command.
//
// Printed to stdout and nowhere else. The private key signs messages claiming
// to come from this inn, and a log line is a copy of it that outlives whatever
// reason there was to look.
func vapid(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("vapid: takes no arguments")
	}

	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("vapid: generating a key pair: %w", err)
	}

	fmt.Fprintf(os.Stdout, `
Put these in the environment. Both are needed; neither is useful alone.

PUSH_VAPID_PUBLIC_KEY=%s
PUSH_VAPID_PRIVATE_KEY=%s

The public key goes out to browsers and can do nothing on its own. The private
key signs deliveries and must not be logged, committed, or shared.

Generating another pair turns every phone's notifications off — they subscribe
against the public key, and the push service will not accept anything signed by
a different one.
`, public, private)

	return nil
}
