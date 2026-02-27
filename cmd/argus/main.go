package main

import (
	"log"

	"github.com/acmeorg/argus/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
