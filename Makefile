# VERSION defines the project version.
GIT_HEAD_COMMIT ?= $(shell git rev-parse --short HEAD)
VERSION         ?= $(or $(shell git describe --abbrev=0 --tags 2>/dev/null),$(GIT_HEAD_COMMIT))

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

MODULE_PATH  ?= $(shell head -n1 go.mod | awk '{print $$2}')
CHART_NAME   ?= ssm-annotation-resolver
LOCALBIN     ?= $(shell pwd)/bin

CONTAINER_REPOSITORY ?= ghcr.io/mikemajesty/$(CHART_NAME)
CHART_REPOSITORY     ?= oci://ghcr.io/mikemajesty/charts
KO_LD_FLAGS          ?= "-X $(MODULE_PATH)/pkg.GitTag=$(VERSION)"

KO             ?= $(LOCALBIN)/ko
YQ             ?= $(LOCALBIN)/yq
HELM           ?= $(LOCALBIN)/helm
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT  ?= $(LOCALBIN)/golangci-lint

KO_VERSION             ?= v0.18.1
YQ_VERSION             ?= v4.44.2
HELM_VERSION           ?= v4.1.1
CONTROLLER_GEN_VERSION ?= v0.20.0
GOLANGCI_LINT_VERSION  ?= v2.0.2

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: help
help:
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run Go tests
	go test ./... -v -timeout 5m

.PHONY: lint
lint: golangci-lint ## Run golangci-lint
	$(GOLANGCI_LINT) run -c .golangci.yml

.PHONY: rbac
rbac: controller-gen yq ## Generate RBAC rules for Helm chart
	$(CONTROLLER_GEN) rbac:roleName=$(CHART_NAME)-role paths="./..." output:stdout | $(YQ) '.rules' > ./charts/$(CHART_NAME)/controller-gen/clusterrole.yaml

.PHONY: render
render: helm ## Render Helm chart
	$(HELM) template $(CHART_NAME) ./charts/$(CHART_NAME)

.PHONY: build
build: ko ## Build local image without push
	LD_FLAGS=$(KO_LD_FLAGS) \
	KOCACHE=/tmp/ko-cache KO_DOCKER_REPO=$(CONTAINER_REPOSITORY) \
	$(KO) build ./ --bare --tags=$(VERSION) --local=true

.PHONY: push
push: ko ## Build and push image
	LD_FLAGS=$(KO_LD_FLAGS) \
	KOCACHE=/tmp/ko-cache KO_DOCKER_REPO=$(CONTAINER_REPOSITORY) \
	$(KO) build ./ --bare --tags=$(VERSION) --push=true --sbom=none

.PHONY: chart
chart: helm yq ## Package and push Helm chart
	$(YQ) -i ".appVersion = \"$(VERSION)\" | .version = (.appVersion | sub(\"^v\"; \"\"))" ./charts/$(CHART_NAME)/Chart.yaml
	$(HELM) package ./charts/$(CHART_NAME) --destination ./dist
	$(HELM) push ./dist/$(CHART_NAME)-$(patsubst v%,%,$(VERSION)).tgz $(CHART_REPOSITORY)

.PHONY: helm
helm: $(HELM)
$(HELM): $(LOCALBIN)
	test -s $(HELM) || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" helm.sh/helm/v4/cmd/helm@$(HELM_VERSION)

.PHONY: ko
ko: $(KO)
$(KO): $(LOCALBIN)
	test -s $(KO) || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/google/ko@$(KO_VERSION)

.PHONY: yq
yq: $(YQ)
$(YQ): $(LOCALBIN)
	test -s $(YQ) || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/mikefarah/yq/v4@$(YQ_VERSION)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(CONTROLLER_GEN) || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(GOLANGCI_LINT) || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
