# VERSION defines the project version for the bundle.
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
GIT_HEAD_COMMIT ?= $(shell git rev-parse --short HEAD)
VERSION         ?= $(or $(shell git describe --abbrev=0 --tags 2>/dev/null),$(GIT_HEAD_COMMIT))

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
KIND           ?= $(LOCALBIN)/kind
KUBECTL        ?= $(LOCALBIN)/kubectl
SETUP_ENVTEST  ?= $(LOCALBIN)/setup-envtest

## Tool Versions
KIND_VERSION          ?= v0.31.0
KIND_CLUSTER          ?= $(MODULE_NAME)
KUBECTL_VERSION       ?= v1.35.0
HELM_VERSION          ?= v4.1.1
KO_VERSION            ?= v0.18.1
YQ_VERSION            ?= v4.44.2
CONTROLLER_GEN_VERSION ?= v0.20.0
GOLANGCI_LINT_VERSION ?= v2.0.2
SETUP_ENVTEST_VERSION ?= latest
HELM_DOCS_VERSION     ?= v1.8.1

## Deploy config
NAMESPACE ?= $(MODULE_NAME)-system

# Replace with your go path:
# must match the go.mod one.
KO_LD_FLAGS ?= "-X github.com/clastix/$(MODULE_NAME)/pkg.GitTag=$(VERSION)"
KO_PUSH     ?= false
KO_LOCAL    ?= true

CRD_DIR = ./charts/$(MODULE_NAME)/crds

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

.PHONY: kind-load-image
kind-load-image: build $(KIND) ## Build and load the image into the kind cluster.
	$(KIND) load docker-image $(CONTAINER_REPOSITORY):$(VERSION) --name $(KIND_CLUSTER)

##@ Development

.PHONY: test
test: lint unit-test e2e-test ## Run lint, unit tests, and e2e tests.

.PHONY: unit-test
unit-test: ## Run Go unit tests (non-e2e).
	go test ./pkg/... -v -timeout 2m

.PHONY: e2e-test
e2e-test: setup-envtest crds ## Run e2e controller tests using envtest.
	KUBEBUILDER_ASSETS="$(shell $(SETUP_ENVTEST) use -p path)" \
	go test ./test/e2e/... -v -timeout 5m

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: crds
crds: controller-gen ## Generate CRD manifests into charts/$(MODULE_NAME)/crds/.
	$(CONTROLLER_GEN) crd paths="./..." output:crd:dir=$(CRD_DIR)

.PHONY: install
install: $(KUBECTL) ## Apply CRD manifests to the current cluster context.
	$(KUBECTL) apply -f $(CRD_DIR)

.PHONY: deploy
deploy: helm rbac crds ## Deploy the controller into the cluster via Helm.
	$(HELM) upgrade --install $(MODULE_NAME) ./charts/$(MODULE_NAME) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.tag=$(VERSION) \
		--set image.pullPolicy=Never

.PHONY: run
run: generate crds install ## Generate deepcopy + CRDs, apply them, then run the controller locally.
	go run main.go

.PHONY: helm-docs
helm-docs: ## Generate the Helm documentation.
	@docker run --rm -v "$$(pwd)/charts/$(MODULE_NAME):/helm-docs:z" -u $$(id -u) jnorwood/helm-docs:$(HELM_DOCS_VERSION)

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
	test -s $(LOCALBIN)/helm || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" helm.sh/helm/v4/cmd/helm@$(HELM_VERSION)

.PHONY: ko
ko: $(KO) ## Download ko locally if necessary.
$(KO): $(LOCALBIN)
	test -s $(LOCALBIN)/ko || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/google/ko@$(KO_VERSION)

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): $(LOCALBIN)
	test -s $(LOCALBIN)/yq || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/mikefarah/yq/v4@$(YQ_VERSION)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install -ldflags="-s -w" github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: setup-envtest
setup-envtest: $(SETUP_ENVTEST) ## Download setup-envtest locally if necessary.
$(SETUP_ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION)

.PHONY: kind
kind: $(KIND) ## Download kind locally if necessary.
$(KIND): $(LOCALBIN)
	@if ! test -s $(LOCALBIN)/kind; then \
		echo "Downloading kind $(KIND_VERSION)..."; \
		if [ "$$(uname -m)" = x86_64 ]; then \
			curl -Lo $(LOCALBIN)/kind https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-linux-amd64; \
		elif [ "$$(uname -m)" = aarch64 ]; then \
			curl -Lo $(LOCALBIN)/kind https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-linux-arm64; \
		else \
			echo "Unsupported architecture: $$(uname -m)"; exit 1; \
		fi; \
		chmod +x $(LOCALBIN)/kind; \
	fi

.PHONY: kubectl
kubectl: $(KUBECTL) ## Download kubectl locally if necessary.
$(KUBECTL): $(LOCALBIN)
	@if ! test -s $(LOCALBIN)/kubectl; then \
		echo "Downloading kubectl $(KUBECTL_VERSION)..."; \
		if [ "$$(uname -m)" = x86_64 ]; then \
			curl -Lo $(LOCALBIN)/kubectl https://dl.k8s.io/release/$(KUBECTL_VERSION)/bin/linux/amd64/kubectl; \
		elif [ "$$(uname -m)" = aarch64 ]; then \
			curl -Lo $(LOCALBIN)/kubectl https://dl.k8s.io/release/$(KUBECTL_VERSION)/bin/linux/arm64/kubectl; \
		else \
			echo "Unsupported architecture: $$(uname -m)"; exit 1; \
		fi; \
		chmod +x $(LOCALBIN)/kubectl; \
	fi
