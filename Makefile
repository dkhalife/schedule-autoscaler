SHELL := /bin/sh

APP ?= schedule-autoscaler
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
IMAGE_REPOSITORY ?= ghcr.io/dkhalife/$(APP)
IMAGE ?= $(IMAGE_REPOSITORY):$(VERSION)
MAIN_PACKAGE ?= ./cmd/controller
CHART ?= charts/schedule-autoscaler
DIST ?= dist

.PHONY: all build test fmt-check validate-yaml helm-lint verify docker-build docker-push \
	install-crds install uninstall package clean

all: verify build

build:
	CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)" \
		-o bin/$(APP) $(MAIN_PACKAGE)

test:
	go test ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Go files are not formatted"; gofmt -l .; exit 1)

validate-yaml:
	python scripts/validate_yaml.py

helm-lint:
	helm lint $(CHART) --strict
	helm template $(APP) $(CHART) --namespace schedule-autoscaler-system \
		| python scripts/validate_yaml.py -

verify: fmt-check test validate-yaml helm-lint

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg MAIN_PACKAGE=$(MAIN_PACKAGE) \
		-t $(IMAGE) .

docker-push: docker-build
	docker push $(IMAGE)

install-crds:
	kubectl apply --server-side -f config/crd/bases/dkhalife.dev_schedules.yaml

install:
	helm upgrade --install $(APP) $(CHART) \
		--namespace schedule-autoscaler-system --create-namespace \
		--set image.repository=$(IMAGE_REPOSITORY) \
		--set image.tag=$(VERSION)

uninstall:
	helm uninstall $(APP) --namespace schedule-autoscaler-system

package:
	mkdir -p $(DIST)
	helm package $(CHART) --destination $(DIST)

clean:
	rm -rf bin $(DIST)
