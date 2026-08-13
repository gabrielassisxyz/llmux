package main

import (
	"fmt"
	"os"
)

func main() {
	_, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup failed: %v\n", err)
		os.Exit(1)
	}
}
