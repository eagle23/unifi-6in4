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
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "mkdir -p $(REMOTE_DIR)"
	scp bin/$(BINARY_NAME) $(ROUTER_USER)@$(ROUTER_HOST):/tmp/$(BINARY_NAME).new
	scp scripts/tunnel.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/tunnel.sh.new
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "pkill -f '^$(REMOTE_DIR)/$(BINARY_NAME)($$| )' || true; sleep 1; mv /tmp/$(BINARY_NAME).new $(REMOTE_DIR)/$(BINARY_NAME); mv /tmp/tunnel.sh.new $(REMOTE_DIR)/tunnel.sh; chmod +x $(REMOTE_DIR)/$(BINARY_NAME) $(REMOTE_DIR)/tunnel.sh; $(REMOTE_DIR)/tunnel.sh boot"

install: build
	scp scripts/install.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp bin/$(BINARY_NAME) $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp scripts/tunnel.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp scripts/rc-local-fragment.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "chmod +x /tmp/install.sh && /tmp/install.sh"

clean:
	rm -rf bin/
