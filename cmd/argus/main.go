package main

import (
	"log"

	"github.com/BeLazy167/argus/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
