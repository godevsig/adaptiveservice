SHELL=bash

.PHONY: client servers mtracer chans

runc: client ## Run client
	@./tclient -d | tee tclient.log

runs: servers mtracer ## Run servers
	@./tserviceA -d > tserviceA.log &
	@./tserviceB -d > tserviceB.log &
	@./tserviceC -d > tserviceC.log &
	@./tserviceD -d > tserviceD.log &
	@./tmsgtracer -d > tmsgtracer.log &
	@tail -f tmsgtracer.log  tservice*.log

runchs: chans mtracer ## Run chan servers
	@./tservices -d > tservices.log &
	@./tmsgtracer -d > tmsgtracer.log &
	@tail -f tmsgtracer.log tservices.log

chans: ## Build chan server all in one
	@go build -o tservices oneserver/main.go

client: ## Build client
	@go build -o tclient client/client.go

servers: ## Build servers
	@go build -o tserviceA service_a/server/server.go
	@go build -o tserviceB service_b/server/server.go
	@go build -o tserviceC service_c/server/server.go
	@go build -o tserviceD service_d/server/server.go

mtracer: ## Build message tracer
	@go build -o tmsgtracer msgtracer/main.go

clean: ## Clean
	@pkill -SIGINT tservice;pkill -SIGINT tmsgtracer;true
	@rm -f tservice* tclient tmsgtracer *.log

help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
