# Set DEBUGGER=1 to build debug symbols
LDFLAGS = $(if $(DEBUGGER),,-s -w) $(shell ./hack/version.sh)

# SET DOCKER_REGISTRY to change the docker registry
DOCKER_REGISTRY := $(if $(DOCKER_REGISTRY),$(DOCKER_REGISTRY),localhost:5000)

GOVER_MAJOR := $(shell go version | sed -E -e "s/.*go([0-9]+)[.]([0-9]+).*/\1/")
GOVER_MINOR := $(shell go version | sed -E -e "s/.*go([0-9]+)[.]([0-9]+).*/\2/")
GO113 := $(shell [ $(GOVER_MAJOR) -gt 1 ] || [ $(GOVER_MAJOR) -eq 1 ] && [ $(GOVER_MINOR) -ge 13 ]; echo $$?)
ifeq ($(GO113), 1)
$(error Please upgrade your Go compiler to 1.13 or higher version)
endif

# Enable GO111MODULE=on explicitly, disable it with GO111MODULE=off when necessary.
export GO111MODULE := on
GOOS := $(if $(GOOS),$(GOOS),linux)
GOARCH := $(if $(GOARCH),$(GOARCH),amd64)
GOENV  := GO15VENDOREXPERIMENT="1" CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH)
GO     := $(GOENV) go
GO_BUILD := $(GO) build -trimpath

IMAGE_TAG ?= latest
PACKAGE_LIST := go list ./... | grep -vE "client/(clientset|informers|listers)"
PACKAGE_DIRECTORIES := $(PACKAGE_LIST) | sed 's|github.com/pingcap/tidb-operator/||'
FILES := $$(find $$($(PACKAGE_DIRECTORIES)) -name "*.go")
FAIL_ON_STDOUT := awk '{ print } END { if (NR > 0) { exit 1 } }'
TEST_COVER_PACKAGES:=go list ./pkg/... | grep -vE "pkg/client" | grep -vE "pkg/tkctl" | grep -vE "pkg/apis" | sed 's|github.com/pingcap/tidb-operator/|./|' | tr '\n' ','

default: build

docker-push: docker backup-docker
	docker push "${DOCKER_REGISTRY}/pingcap/tidb-operator:${IMAGE_TAG}"
	docker push "${DOCKER_REGISTRY}/pingcap/tidb-backup-manager:${IMAGE_TAG}"

ifeq ($(NO_BUILD),y)
docker:
	@echo "NO_BUILD=y, skip build for $@"
else
docker: build
endif
	docker build --tag "${DOCKER_REGISTRY}/pingcap/tidb-operator:${IMAGE_TAG}" images/tidb-operator

build: controller-manager scheduler discovery admission-controller apiserver backup-manager

controller-manager:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o images/tidb-operator/bin/tidb-controller-manager cmd/controller-manager/main.go

scheduler:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o images/tidb-operator/bin/tidb-scheduler cmd/scheduler/main.go

discovery:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o images/tidb-operator/bin/tidb-discovery cmd/discovery/main.go

admission-controller:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o images/tidb-operator/bin/tidb-admission-controller cmd/admission-controller/main.go

apiserver:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o images/tidb-operator/bin/tidb-apiserver cmd/apiserver/main.go

backup-manager:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o images/backup-manager/bin/tidb-backup-manager cmd/backup-manager/main.go

ifeq ($(NO_BUILD),y)
backup-docker:
	@echo "NO_BUILD=y, skip build for $@"
else
backup-docker: backup-manager
endif
	docker build --tag "${DOCKER_REGISTRY}/pingcap/tidb-backup-manager:${IMAGE_TAG}" images/backup-manager

e2e-docker-push: e2e-docker test-apiserver-dokcer-push
	docker push "${DOCKER_REGISTRY}/pingcap/tidb-operator-e2e:${IMAGE_TAG}"

ifeq ($(NO_BUILD),y)
e2e-docker:
	@echo "NO_BUILD=y, skip build for $@"
else
e2e-docker: e2e-build
endif
	[ -d tests/images/e2e/tidb-operator ] && rm -r tests/images/e2e/tidb-operator || true
	[ -d tests/images/e2e/tidb-cluster ] && rm -r tests/images/e2e/tidb-cluster || true
	[ -d tests/images/e2e/tidb-backup ] && rm -r tests/images/e2e/tidb-backup || true
	[ -d tests/images/e2e/manifests ] && rm -r tests/images/e2e/manifests || true
	cp -r charts/tidb-operator tests/images/e2e
	cp -r charts/tidb-cluster tests/images/e2e
	cp -r charts/tidb-backup tests/images/e2e
	cp -r manifests tests/images/e2e
	docker build -t "${DOCKER_REGISTRY}/pingcap/tidb-operator-e2e:${IMAGE_TAG}" tests/images/e2e

e2e-build: test-apiserver-build
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o tests/images/e2e/bin/ginkgo github.com/onsi/ginkgo/ginkgo
	$(GO) test -c -ldflags '$(LDFLAGS)' -o tests/images/e2e/bin/e2e.test ./tests/e2e

test-apiserver-dokcer-push: test-apiesrver-docker
	docker push "${DOCKER_REGISTRY}/pingcap/test-apiserver:${IMAGE_TAG}"

ifeq ($(NO_BUILD),y)
test-apiesrver-docker:
	@echo "NO_BUILD=y, skip build for $@"
else
test-apiesrver-docker: test-apiserver-build
endif
	docker build -t "${DOCKER_REGISTRY}/pingcap/test-apiserver:${IMAGE_TAG}" tests/images/test-apiserver

test-apiserver-build:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o tests/images/test-apiserver/bin/tidb-apiserver tests/cmd/apiserver/main.go

e2e:
	./hack/e2e.sh

stability-test-build:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o tests/images/stability-test/bin/stability-test tests/cmd/stability/*.go

stability-test-docker: stability-test-build
	docker build -t "${DOCKER_REGISTRY}/pingcap/tidb-operator-stability-test:${IMAGE_TAG}" tests/images/stability-test

stability-test-push: stability-test-docker
	docker push "${DOCKER_REGISTRY}/pingcap/tidb-operator-stability-test:${IMAGE_TAG}"

fault-trigger:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o tests/images/fault-trigger/bin/fault-trigger tests/cmd/fault-trigger/*.go

# ARGS:
#
# GOFLAGS: Extra flags to pass to 'go' when building, e.g.
# 		`-v` for verbose logging.
# 		`-race` for race detector.
# GO_COVER: Whether to run tests with code coverage. Set to 'y' to enable coverage collection.
#
ifeq ($(GO_COVER),y)
test:
	@echo "Run unit tests"
	@go test -cover ./pkg/... -coverpkg=$$($(TEST_COVER_PACKAGES)) -coverprofile=coverage.txt -covermode=atomic && echo "\nUnit tests run successfully!"
else
test:
	@echo "Run unit tests"
	@go test ./pkg/... && echo "\nUnit tests run successfully!"
endif

check-all: lint check-static check-shadow check-gosec staticcheck errcheck

check-setup:
	@which retool >/dev/null 2>&1 || GO111MODULE=off go get github.com/twitchtv/retool
	@GO111MODULE=off retool sync

check: check-setup lint tidy check-static check-codegen check-terraform

check-static:
	@ # Not running vet and fmt through metalinter becauase it ends up looking at vendor
	@echo "gofmt checking"
	gofmt -s -l -w $(FILES) 2>&1| $(FAIL_ON_STDOUT)
	@echo "go vet check"
	@go vet -all $$($(PACKAGE_LIST)) 2>&1
	@echo "mispell and ineffassign checking"
	CGO_ENABLED=0 retool do gometalinter.v2 --disable-all \
	  --enable misspell \
	  --enable ineffassign \
	  $$($(PACKAGE_DIRECTORIES))

check-codegen:
	./hack/verify-codegen.sh

check-terraform:
	./hack/check-terraform.sh
	git diff --quiet deploy

# TODO: staticcheck is too slow currently
staticcheck:
	@echo "gometalinter staticcheck"
	CGO_ENABLED=0 retool do gometalinter.v2 --disable-all --deadline 120s \
	  --enable staticcheck \
	  $$($(PACKAGE_DIRECTORIES))

# TODO: errcheck is too slow currently
errcheck:
	@echo "gometalinter errcheck"
	CGO_ENABLED=0 retool do gometalinter.v2 --disable-all --deadline 120s \
	  --enable errcheck \
	  $$($(PACKAGE_DIRECTORIES))

# TODO: shadow check fails at the moment
check-shadow:
	@echo "go vet shadow checking"
	go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow
	@go vet -vettool=$(which shadow) $$($(PACKAGE_LIST))

lint:
	@echo "linting"
	CGO_ENABLED=0 retool do revive -formatter friendly -config revive.toml $$($(PACKAGE_LIST))

tidy:
	@echo "go mod tidy"
	go mod tidy
	git diff -U --exit-code go.mod go.sum

check-gosec:
	@echo "security checking"
	CGO_ENABLED=0 retool do gosec $$($(PACKAGE_DIRECTORIES))

cli:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o tkctl cmd/tkctl/main.go

debug-docker-push: debug-build-docker
	docker push "${DOCKER_REGISTRY}/pingcap/debug-launcher:latest"
	docker push "${DOCKER_REGISTRY}/pingcap/tidb-control:latest"
	docker push "${DOCKER_REGISTRY}/pingcap/tidb-debug:latest"

debug-build-docker: debug-build
	docker build -t "${DOCKER_REGISTRY}/pingcap/debug-launcher:latest" misc/images/debug-launcher
	docker build -t "${DOCKER_REGISTRY}/pingcap/tidb-control:latest" misc/images/tidb-control
	docker build -t "${DOCKER_REGISTRY}/pingcap/tidb-debug:latest" misc/images/tidb-debug

debug-build:
	$(GO_BUILD) -ldflags '$(LDFLAGS)' -o misc/images/debug-launcher/bin/debug-launcher misc/cmd/debug-launcher/main.go

.PHONY: check check-setup check-all build e2e-build debug-build cli e2e
