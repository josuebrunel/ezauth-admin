.PHONY: build test lint templ-gen css tidy docker-up docker-down clean

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

# Dev-time only. The built admin/static/admin.css is committed and embedded.
css:
	npm install --no-audit --no-fund
	npm run css

tidy:
	go mod tidy

docker-up:
	docker compose up -d

docker-down:
	docker compose down

clean:
	rm -rf node_modules coverage.txt
