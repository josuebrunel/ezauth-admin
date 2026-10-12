.PHONY: build test lint templ-gen css run-example tidy docker-up docker-down clean

# Pinned dev-time CSS tooling. No Node: Tailwind ships a standalone binary and
# DaisyUI ships its plugin as a plain file. Both are downloaded on demand.
TAILWIND_VERSION := 4.3.3
DAISYUI_VERSION  := 5.7.47
OS   := $(shell uname -s | tr '[:upper:]' '[:lower:]' | sed 's/darwin/macos/')
ARCH := $(shell uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/')
TAILWIND := bin/tailwindcss-$(TAILWIND_VERSION)

# The panel is a library: build compiles every package. There is no binary
# and no migrations of its own, it uses ezauth's schema.
build: templ-gen
	go build ./...

test: templ-gen
	go test ./... -count=1

lint:
	go vet ./...
	golangci-lint run

templ-gen:
	templ generate

$(TAILWIND):
	mkdir -p bin
	curl -fsSL -o $@ https://github.com/tailwindlabs/tailwindcss/releases/download/v$(TAILWIND_VERSION)/tailwindcss-$(OS)-$(ARCH)
	chmod +x $@

admin/styles/daisyui.mjs admin/styles/daisyui-theme.mjs:
	curl -fsSL -o $@ https://github.com/saadeghi/daisyui/releases/download/v$(DAISYUI_VERSION)/$(@F)

# Dev-time only. The built admin/static/admin.css is committed and embedded.
css: $(TAILWIND) admin/styles/daisyui.mjs admin/styles/daisyui-theme.mjs
	$(TAILWIND) -i admin/styles/input.css -o admin/static/admin.css --minify

run-example:
	go run ./examples/chi

tidy:
	go mod tidy

docker-up:
	docker compose up -d

docker-down:
	docker compose down

clean:
	rm -rf bin data coverage.txt
