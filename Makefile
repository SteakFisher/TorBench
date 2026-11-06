SYSTEM ?= torment
FEATURES ?= default
ARGS ?=
URL_torment = http://localhost:8088
URL_formance = http://localhost:3068

.PHONY: up down fresh seed run report test

# make up SYSTEM=formance
up:
	docker compose --profile $(SYSTEM) up -d --build --wait
	until curl -sf $(URL_$(SYSTEM))/$(if $(filter formance,$(SYSTEM)),_/info,healthz) >/dev/null; do sleep 1; done

down:
	docker compose --profile torment --profile formance down

# fresh wipes every volume and starts SYSTEM empty.
fresh:
	docker compose --profile torment --profile formance down -v
	$(MAKE) up SYSTEM=$(SYSTEM)

seed:
	go run ./cmd/torbench seed -system $(SYSTEM) -features $(FEATURES) $(ARGS)

# make run SYSTEM=formance ARGS="-rate 500 -duration 1m"
run:
	go run ./cmd/torbench run -system $(SYSTEM) -features $(FEATURES) $(ARGS)

# make report RESULTS='results/aws/*.json'
RESULTS ?= results/*/*.json
report:
	go run ./cmd/torbench report $(RESULTS) > results/report.html

test:
	go test ./...
