# Run `make` or `make help` for a description of every target.
#
# Targets are documented inline: a `## comment` after a target's colon becomes its
# help text, and `##@ Section` lines group them. Keep new targets annotated so they
# show up in the help output.

.DEFAULT_GOAL := help

.PHONY: help dev build run test test-backend test-frontend wipe-target clear-state docker-build docker-run clean

##@ General

help: ## Show this help
	@awk 'BEGIN { \
			FS = ":.*##"; \
			printf "\nDoppler On-Prem Migrator\n\nUsage:\n  make \033[36m<target>\033[0m\n"; \
		} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5); next } \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo ""

##@ Development

dev: ## Start the Vite dev server (:5173) and the Go backend (:8080) together
	./scripts/dev.sh

##@ Build & Run

build: ## Build the frontend and compile the self-contained binary to backend/server
	./scripts/build.sh

run: ## Run the compiled binary on http://127.0.0.1:8080 (run `make build` first)
	@test -x backend/server || { \
		echo "backend/server not found. Run 'make build' first." >&2; \
		exit 1; \
	}
	@# The exec bit alone is not enough: a devcontainer shares this checkout with
	@# the host, so a binary built on one side sits in the other side's tree and
	@# fails with a bare "cannot execute binary file". Compare what it was built
	@# for against what we are running on, and say so plainly.
	@built_os=$$(go version -m backend/server 2>/dev/null | awk -F= '/GOOS=/{print $$2}'); \
	built_arch=$$(go version -m backend/server 2>/dev/null | awk -F= '/GOARCH=/{print $$2}'); \
	host_os=$$(go env GOOS); host_arch=$$(go env GOARCH); \
	if [ -n "$$built_os" ] && { [ "$$built_os" != "$$host_os" ] || [ "$$built_arch" != "$$host_arch" ]; }; then \
		echo "backend/server was built for $$built_os/$$built_arch, but this is $$host_os/$$host_arch." >&2; \
		echo "Run 'make build' to rebuild it here." >&2; \
		exit 1; \
	fi
	./backend/server

##@ Testing

test: test-backend test-frontend ## Run the backend and frontend test suites

test-backend: ## Run the Go test suite
	cd backend && go test ./... -v

test-frontend: ## Run the Vitest suite
	cd frontend && pnpm test

wipe-target: ## Empty the "Migrator Target" workplace for another test run (dry run; ARGS="--yes --clear-state" to delete)
	@# Needs TARGET_DOPPLER_TOKEN, and TARGET_DOPPLER_HOST for anything but
	@# Doppler's own API. The state directory is the one `make run` uses — the
	@# server resolves ./data from the repo root, and `go run` would resolve it
	@# from backend/ — so --clear-state clears the files the server wrote.
	cd backend && go run ./cmd/wipe-target --state-dir "$${CHECKPOINT_DIR:-$(CURDIR)/data}" $(ARGS)

clear-state: ## Forget every migration run from this checkout, without touching any workplace (stop the server first)
	@# Run through bash rather than executed directly, so it works whether or not
	@# the script's exec bit survived the checkout.
	bash ./scripts/clear-state.sh

##@ Docker

docker-build: ## Build the distroless container image as doppler-migrator
	docker build -t doppler-migrator .

docker-run: ## Start the container via Docker Compose on :8080
	docker compose up

##@ Housekeeping

clean: ## Remove the binary, frontend build output, embedded assets, and node_modules
	rm -f backend/server
	rm -rf frontend/dist frontend/node_modules
	@# Clear the embed directory but keep .gitkeep — see backend/embed.go.
	find backend/webui -mindepth 1 -not -name '.gitkeep' -delete 2>/dev/null || true
