-include .env

DB_DSN := "postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(SSL_MODE)"

.PHONY: db-up
db-up:
	@echo "upgrading db up to the last revision"
	migrate -database $(DB_DSN) -path ./migrations up

.PHONY: db-down
db-down:
	@echo "downgrading db one revision down"
	migrate -database $(DB_DSN) -path ./migrations down
