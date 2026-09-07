package main

import (
	"log"

	"github.com/artni96/GophProfile/internal/config"
)

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		log.Fatal("failed to init config:\n", err)
	}
	err = run(cfg)
	if err != nil {
		log.Fatal(err)
	}
}
