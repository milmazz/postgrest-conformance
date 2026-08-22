DB_CONTAINER ?= oracle-pg
DB_PORT ?= 6432
# postgresql-17-pg-safeupdate does not exist in the PGDG apt repo (verified:
# apt-cache search returns nothing under either name on the postgis/postgis
# Debian bullseye base), so db-up compiles it from source instead, pinned to
# the latest pg-safeupdate release tag (no GitHub Releases are published for
# this repo, only tags: https://github.com/eradman/pg-safeupdate/tags).
SAFEUPDATE_REF ?= 1.7

.PHONY: test build fetch db-up db-down
build:
	go build ./...
test:
	go test ./...
fetch:
	go run ./cmd/oracle fetch
db-up:
	docker run -d --name $(DB_CONTAINER) --platform=linux/amd64 -p $(DB_PORT):5432 \
	  -e POSTGRES_PASSWORD=postgres postgis/postgis:17-3.5
	@until docker exec $(DB_CONTAINER) pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	docker exec $(DB_CONTAINER) bash -c \
	  'apt-get update -qq && apt-get install -y -qq gcc make git postgresql-server-dev-17 && \
	   cd /tmp && git clone --branch $(SAFEUPDATE_REF) --depth 1 https://github.com/eradman/pg-safeupdate && \
	   make -C pg-safeupdate install'
db-down:
	docker rm -f $(DB_CONTAINER)
