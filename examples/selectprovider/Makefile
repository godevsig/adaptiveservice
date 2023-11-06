SHELL=bash

.PHONY: client server builtin

define do_servers
	@./tbuiltin server $(1) -lister > tbuiltin.log &
	@sleep 1
	@./tservice $(1) > tservice1.log &
	@./tservice $(1) > tservice2.log &
	@./tservice $(1) > tservice3.log &
	@./tservice $(1) > tservice4.log &
	@./tservice $(1) -r 1 -w -1 > tservice11.log &
	@./tservice $(1) -r 2 -w -1 > tservice12.log &
	@./tservice $(1) -r 4 -w -1 > tservice13.log &
	@./tservice $(1) -r 8 -w -1 > tservice14.log &
	@./tservice $(1) -r 16 -w -1 > tservice15.log &
endef

runc: client ## Run client
	@for i in `seq 100`; do (./tclient -n 100000 > /dev/null &); done
	@sleep 10
	@./tclient -d -m1 1 > tclient_cpacity.log &
	@./tclient -d -m1 2 > tclient_latency.log &
	@./tclient -d -m1 1 -m2 2 > tclient_cpacity_latency.log &
	@./tclient -d -m1 2 -m2 1 > tclient_latency_cpacity.log &

runs: server builtin ## Run servers
	$(call do_servers)

debugs: server builtin ## Debug servers
	$(call do_servers, -d)

client: ## Build client
	@go build -o tclient client/client.go

server: ## Build server
	@go build -o tservice selectme/server/server.go

builtin:
	@go build -o tbuiltin ../builtin/builtin.go

clean: ## Clean
	@pkill -SIGINT tbuiltin; pkill -SIGINT tservice;true
	@rm -f tservice tclient tbuiltin *.log

help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
