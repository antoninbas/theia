GO                 ?= go
BINDIR := $(CURDIR)/bin

all: bin

.PHONY: bin
bin:
	GOBIN=$(BINDIR) $(GO) install antrea.io/theia/snowflake/...

.PHONY: test
test:
	$(GO) test -v ./...

.PHONY: clean
clean:
	rm -rf bin
