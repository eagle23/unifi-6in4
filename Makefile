BINARY_NAME=ipv6-tunnel-server
ROUTER_HOST?=192.168.1.1
ROUTER_USER?=root
REMOTE_DIR=/data/ipv6-tunnel

.PHONY: build test deploy clean install

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
		-ldflags="-s -w" \
		-o bin/$(BINARY_NAME) ./cmd/server/

test:
	go test ./... -v

deploy: build
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "mkdir -p $(REMOTE_DIR)/web"
	scp bin/$(BINARY_NAME) $(ROUTER_USER)@$(ROUTER_HOST):$(REMOTE_DIR)/
	scp scripts/tunnel.sh $(ROUTER_USER)@$(ROUTER_HOST):$(REMOTE_DIR)/
	scp web/index.html $(ROUTER_USER)@$(ROUTER_HOST):$(REMOTE_DIR)/web/
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "chmod +x $(REMOTE_DIR)/$(BINARY_NAME) $(REMOTE_DIR)/tunnel.sh"
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "pkill -f $(BINARY_NAME) || true; $(REMOTE_DIR)/$(BINARY_NAME) &"

install: build
	scp scripts/install.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp bin/$(BINARY_NAME) $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp scripts/tunnel.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp scripts/rc-local-fragment.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp web/index.html $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "chmod +x /tmp/install.sh && /tmp/install.sh"

clean:
	rm -rf bin/
