# VERSION defines the project version for the bundle.
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
VERSION ?= $(or $(shell git describe --abbrev=0 --tags 2>/dev/null),$(GIT_HEAD_COMMIT))

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

# Replace with your container repository FQDN
MODULE_NAME          ?= $(shell head -n1 go.mod | sed 's|module.*/||')
CONTAINER_REPOSITORY ?= ghcr.io/clastix/$(MODULE_NAME)

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KO             ?= $(LOCALBIN)/ko
YQ             ?= $(LOCALBIN)/yq
HELM           ?= $(LOCALBIN)/helm
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT  ?= $(LOCALBIN)/golangci-lint
# Replace with your go path:
# must match the go.mod one.
KO_LD_FLAGS ?= "-X github.com/clastix/$(MODULE_NAME)/pkg.GitTag=$(VERSION)"
KO_PUSH     ?= false
KO_LOCAL    ?= true

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Build

.PHONY: push
push: $(KO) ## Push container image.
	LD_FLAGS=$(KO_LD_FLAGS) \
	KOCACHE=/tmp/ko-cache KO_DOCKER_REPO=${CONTAINER_REPOSITORY} \
	$(KO) build ./ --bare --tags=$(VERSION) --push=true --sbom=none

.PHONY: chart
chart: $(HELM) $(YQ) ## Build the Helm Chart according to the current version.
	$(YQ) -i ".appVersion = \"$(VERSION)\" | .version = (.appVersion | sub(\"^v\"; \"\"))" ./charts/$(MODULE_NAME)/Chart.yaml
	$(MAKE) helm-docs
	$(HELM) package ./charts/$(MODULE_NAME) --destination ./dist
	$(HELM) push dist/$(MODULE_NAME)-$(patsubst v%,%,$(VERSION)).tgz oci://ghcr.io/clastix/charts

.PHONY: build
build: $(KO) ## Build local image without pushing.
	LD_FLAGS=$(KO_LD_FLAGS) \
	KOCACHE=/tmp/ko-cache KO_DOCKER_REPO=${CONTAINER_REPOSITORY} \
	$(KO) build ./ --bare --tags=$(VERSION) --local=true

##@ Development

.PHONY: helm-docs
helm-docs: ## Generate the Helm documentation.
	@docker run --rm -v "$$(pwd)/charts/$(MODULE_NAME):/helm-docs" -u $$(id -u) jnorwood/helm-docs:v1.8.1

.PHONY: rbac
rbac: controller-gen yq ## Reads the kubebuilder marker and generate the resulting RBAC for the Helm Chart.
	$(CONTROLLER_GEN) rbac:roleName=$(MODULE_NAME)-role paths="./..." output:stdout | $(YQ) '.rules' > ./charts/$(MODULE_NAME)/controller-gen/clusterrole.yaml

.PHONY: lint
lint: golangci-lint ## Linting the code according to the styling guide.
	$(GOLANGCI_LINT) run -c .golangci.yml

##@ Binary

.PHONY: helm
helm: $(HELM) ## Download helm locally if necessary.
$(HELM): $(LOCALBIN)
	test -s $(LOCALBIN)/helm || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" helm.sh/helm/v4/cmd/helm@v4.1.1

.PHONY: ko
ko: $(KO) ## Download ko locally if necessary.
$(KO): $(LOCALBIN)
	test -s $(LOCALBIN)/ko || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/google/ko@v0.18.1

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): $(LOCALBIN)
	test -s $(LOCALBIN)/yq || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/mikefarah/yq/v4@v4.44.2

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.0

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.0.2
