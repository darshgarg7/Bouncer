package main

import (
	"errors"
	"flag"
	"log"
	"os"
)

func main() {
	options, err := parseOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Printf("bouncer run configuration failed: %v", err)
		os.Exit(2)
	}
	if err := run(options); err != nil {
		log.Printf("bouncer run failed: %v", err)
		os.Exit(1)
	}
}
