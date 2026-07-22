.PHONY: build install install-skill clean

BINARY     := turo
PREFIX     ?= /usr/local
BINDIR     := $(PREFIX)/bin
SKILLDIR   ?= $(HOME)/.agents/skills/turo

build:
	go build -ldflags="-s -w" -o $(BINARY) .

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

install-skill:
	install -d $(SKILLDIR)
	install -m 644 skills/turo/SKILL.md $(SKILLDIR)/SKILL.md

clean:
	rm -f $(BINARY)
