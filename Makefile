# promptcloak developer tasks.
SHELL := /bin/bash
BIN := bin
IMG ?= ghcr.io/coreoptimizer/promptcloak/extproc:0.1.0
NAMESPACE ?= promptcloak-system
CHART := charts/promptcloak

.PHONY: all build test vet tidy run docker-build docker-push deploy undeploy \
	helm-lint helm-template helm-package helm-install clean

all: build

## build the ext_proc binary into ./bin
build:
	@mkdir -p $(BIN)
	go build -o $(BIN)/extproc ./cmd/extproc
	@echo "built: $(BIN)/extproc"

## run unit tests
test:
	go test ./...

## run go vet
vet:
	go vet ./...

## tidy go.mod / go.sum
tidy:
	go mod tidy

## run locally (expects PRESIDIO_URL reachable; uses in-memory vault if VALKEY_ADDR unset)
run: build
	./$(BIN)/extproc

## build the container image
docker-build:
	docker build -t $(IMG) .

## push the container image
docker-push:
	docker push $(IMG)

## apply all manifests (requires Envoy Gateway installed in the cluster)
deploy:
	kubectl apply -k deploy/k8s

## remove all manifests
undeploy:
	kubectl delete -k deploy/k8s --ignore-not-found

## lint the Helm chart
helm-lint:
	helm lint $(CHART)

## render the Helm chart to stdout
helm-template:
	helm template promptcloak $(CHART) -n $(NAMESPACE)

## package the Helm chart into ./dist
helm-package:
	@mkdir -p dist
	helm package $(CHART) -d dist

## install the chart from local sources (requires Envoy Gateway in the cluster)
helm-install:
	helm upgrade --install promptcloak $(CHART) \
		--namespace $(NAMESPACE) --create-namespace

clean:
	rm -rf $(BIN) dist
