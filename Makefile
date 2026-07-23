.PHONY: build install install-skill test clean

BINARY     := turo
PREFIX     ?= /usr/local
BINDIR     := $(PREFIX)/bin
SKILLDIR   ?= $(HOME)/.agents/skills/turo
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .

test:
	go test ./...

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

install-skill:
	install -d $(SKILLDIR)
	install -m 644 skills/turo/SKILL.md $(SKILLDIR)/SKILL.md

clean:
	rm -f $(BINARY)
