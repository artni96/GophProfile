package main

import (
	"log"

	"github.com/artni96/GophProfile/internal/config"
)

//	@title			GophProfile
//	@version		1.0
//	@description	Service for user avatar management
//	@host			localhost:8080
//	@BasePath		/api/v1

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
