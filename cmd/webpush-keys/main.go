// Generates a VAPID pair for operator-managed Web Push configuration.
package main

import (
	"fmt"
	"log"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("KAKERATTA_PUSH_PUBLIC_KEY=%s\nKAKERATTA_PUSH_PRIVATE_KEY=%s\n", public, private)
}
