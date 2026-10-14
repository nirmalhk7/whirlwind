.PHONY: run web

WHIRLWIND_BACKEND_URL ?= http://127.0.0.1:8090

run:
	go run ./cmd/whirlwind

web:
	cd web && WHIRLWIND_BACKEND_URL="$(WHIRLWIND_BACKEND_URL)" npm run dev
