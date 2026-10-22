// Command gen-vapid prints a fresh VAPID key pair for the Web Push feature.
//
// The keys belong to the server that sends the notifications. The browser
// subscribes with the public half; the private half signs every request to
// the push service and must never leave the machine.
//
// The program prints the pair and nothing else: it does not read and does
// not write the .env file. Copy the two lines into the .env of the repo
// yourself — the private key is a secret and the .env is not in Git.
//
//	go run ./cmd/gen-vapid
//
// VAPID_PUBLIC_KEY=...
// VAPID_PRIVATE_KEY=...
//
// Every pair is different. A pair that was published before cannot be
// un-published: generate a new one and let the browsers subscribe again.
package main

import (
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate the VAPID keys: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("# Copy these two lines into your .env file.")
	fmt.Println("# The private key is a secret: never commit it, never paste it anywhere.")
	fmt.Printf("VAPID_PUBLIC_KEY=%s\n", publicKey)
	fmt.Printf("VAPID_PRIVATE_KEY=%s\n", privateKey)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Done. Restart the server so it picks the keys up.")
}
