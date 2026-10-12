.PHONY: run web

KHAN_BACKEND_URL ?= http://127.0.0.1:8090

run:
	go run ./cmd/khan

web:
	cd web && KHAN_BACKEND_URL="$(KHAN_BACKEND_URL)" npm run dev
