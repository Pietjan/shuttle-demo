# Change these variables as necessary.
main_package_path = ./cmd/deskline
binary_name       = deskline
addr              = localhost:8080
tls_addr          = localhost:8443

# The accent colour loom's generated theme is built around. Changing it means
# re-running `make css`, because the value is baked into loom.css rather than
# read at runtime.
accent = indigo

# The compiled stylesheet. It is embedded into the binary, which is what lets
# the Docker image stay on scratch - so `build` depends on it and a stale
# sheet ships rather than a missing one.
stylesheet = assets/static/styles.css

# The CLIs live in their own module so the app does not inherit their
# dependency trees. -modfile runs them from that module without changing the
# working directory, which is what lets a tool act on the module it is
# invoked from.
#
# templ is in there for a reason beyond tidiness: generated code is written
# against a particular runtime, so a CLI on $PATH that is older than the
# templ in go.mod generates code that no longer compiles. Pinned here, the
# two cannot drift.
tools = -modfile=tools/go.mod

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

.PHONY: confirm
confirm:
	@echo -n 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

.PHONY: no-dirty
no-dirty:
	@test -z "$(shell git status --porcelain)"

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## audit: run quality control checks
.PHONY: audit
audit: test
	go mod tidy -diff
	go mod verify
	test -z "$(shell go tool $(tools) gofumpt -l .)"
	go vet ./...
	go tool $(tools) golangci-lint run ./...
	go tool $(tools) govulncheck ./...
	cd tools && go mod tidy -diff
	cd tools && go mod verify

## test: run all tests
# -race is not optional. Component state is per session and needs no locks,
# but the store underneath is shared by every session and written from all of
# them - which is exactly the shape -race exists to catch.
.PHONY: test
test: generate
	go test -race -buildvcs ./...

## test/cover: run all tests and display coverage
.PHONY: test/cover
test/cover: generate
	go test -race -buildvcs -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out

# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## tidy: tidy modfiles and format .go files
.PHONY: tidy
tidy:
	go mod tidy -v
	cd tools && go mod tidy -v
	go tool $(tools) gofumpt -l -w .

## generate: generate Go from the .templ views
.PHONY: generate
generate:
	go tool $(tools) templ generate

## css: compile the stylesheet
# Two commands, because they do different jobs. cmd/css writes loom.css -
# the Tailwind import, loom's theme and its structural CSS, with an @source
# pointing at loom's own module directory so the sheet carries every class
# baked into its components. input.css beside it is hand-written: it imports
# that and adds the @sources this app needs. Read the comments in it before
# changing either.
.PHONY: css
css:
	go run github.com/pietjan/loom/cmd/css -accent $(accent) -o assets/css/loom.css
	tailwindcss -i assets/css/input.css -o $(stylesheet) --minify

## build: build the application
.PHONY: build
build: generate css
	go build -o=bin/$(binary_name) $(main_package_path)

## build/linux: build a static linux/amd64 binary for the container image
# CGO off and static, because the image it goes into is scratch: there is no
# libc in there to dynamically link against, and the failure is at startup
# rather than at build time.
.PHONY: build/linux
build/linux: generate css
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -ldflags='-s -w' -o=bin/$(binary_name)-linux-amd64 $(main_package_path)

## run: run the application on $(addr)
.PHONY: run
run: build
	./bin/$(binary_name) -addr $(addr)

## run/tls: run the application over HTTPS/2 on $(tls_addr)
# Not a flourish. Datastar streams over fetch rather than EventSource, so
# every open page holds one of the browser's ~6 connections per origin for as
# long as it is live - and a console somebody keeps in three tabs is most of
# that budget. Over HTTP/2 every stream shares one connection and the limit
# stops existing, which browsers only speak over TLS.
.PHONY: run/tls
run/tls: build
	./bin/$(binary_name) -addr $(tls_addr) -tls

## run/live: run the application with reloading on file changes
.PHONY: run/live
run/live: 
	go tool $(tools) templ generate --watch --proxy="http://$(addr)" --open-browser=false \
		--cmd="go run $(main_package_path) -addr $(addr)"

# ==================================================================================== #
# OPERATIONS
# ==================================================================================== #

## docker/build: build the container image
# The binary is built here rather than in the image, so the Dockerfile stages
# an artefact instead of carrying a Go toolchain. Everything the app serves -
# the stylesheet included - is embedded in it, so nothing else has to ship.
.PHONY: docker/build
docker/build: build/linux
	docker build -t $(binary_name):latest .

## push: audit, then push to the tracking branch
.PHONY: push
push: confirm audit no-dirty
	git push
