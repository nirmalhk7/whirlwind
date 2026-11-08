.PHONY: dev run dashboard

dev: dashboard

run:
	go run ./cmd/whirlwind --supervisor

dashboard:
	./bin/whirlwind dashboard
