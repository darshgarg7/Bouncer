package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"bouncer/internal/eventlog"
)

func main() {
	path := flag.String("event-log", "", "path to a Bouncer JSONL event log")
	flag.Parse()
	if *path == "" {
		log.Fatal("-event-log is required")
	}
	input, err := os.Open(*path)
	if err != nil {
		log.Fatalf("open event log: %v", err)
	}
	defer input.Close()
	verification, err := eventlog.Verify(input)
	if err != nil {
		log.Fatalf("verify event log: %v", err)
	}
	encoded, err := json.MarshalIndent(verification, "", "  ")
	if err != nil {
		log.Fatalf("encode verification: %v", err)
	}
	fmt.Println(string(encoded))
}
