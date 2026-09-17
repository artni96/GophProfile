.PHONY: help
help:
	@echo "Commands list:"
	@sed -n "s/^##//p" $(MAKEFILE_LIST) | column -t -s ":" | sed -e "s/^/ /"


## run: launches app
.PHONY: run
run:
	@echo "launching app"
	docker compose -f docker-compose.yaml up -d

## stop: stops app
.PHONY: stop
stop:
	@echo "stopping app"
	docker compose -f docker-compose.yaml down


## tests-db-up: creates database for integration tests.
.PHONY: tests-db-up
tests-db-up:
	docker compose -f docker-compose-tests.yaml --env-file .env-tests up -d

## tests-db-down: drops database for integration tests.
.PHONY: tests-db-down
tests-db-down:
	docker compose -f docker-compose-tests.yaml down

## run-tests: runs app tests
.PHONY: tests-run
tests-run:
	@echo "Running tests..."
	GOARCH=arm64 GOOS=darwin go test ./... -cover