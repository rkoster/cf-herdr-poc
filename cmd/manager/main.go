package main

import (
	"log"
	"os"

	"cf-herdr-poc/internal/config"
)

func main() {
	if _, err := config.Load(os.Getenv); err != nil {
		log.Fatalf("load manager config: %v", err)
	}
}
