# Store tooling in a location that does not affect the system.
GOBIN := $(CURDIR)/.gobin
export GOBIN
PATH := $(GOBIN):$(PATH)
export PATH

SHELL = env GOBIN=$(GOBIN) PATH=$(PATH) /bin/bash
BASE_DIR=$(CURDIR)

TAG := # make sure tag is never injectable as an env var

ifdef CI
ifneq ($(CIRCLE_TAG),)
TAG := $(CIRCLE_TAG)
endif
endif

ifeq ($(TAG),)
TAG=$(shell git describe --tags --abbrev=10 --dirty --long)
endif

LOGLEVEL="${LOGLEVEL:-DEBUG}"

FORMATTING_FILES=$(shell git grep -L '^// Code generated by .* DO NOT EDIT\.' -- '*.go')

BUILD_DIR_HASH := $(shell git ls-files -sm build | git hash-object --stdin)
BUILD_IMAGE := stackrox/scanner:builder-$(BUILD_DIR_HASH)

ifdef CI
    QUAY_REPO := rhacs-eng
    BUILD_IMAGE := quay.io/$(QUAY_REPO)/scanner:builder-$(BUILD_DIR_HASH)
endif

LOCAL_VOLUME_ARGS := -v$(CURDIR):/src:delegated -v $(GOPATH):/go:delegated
GOPATH_WD_OVERRIDES := -w /src -e GOPATH=/go
BUILD_FLAGS := -e CGO_ENABLED=1,GOOS=linux,GOARCH=amd64
BUILD_CMD := go build -ldflags="-linkmode=external -X github.com/stackrox/scanner/pkg/version.Version=$(TAG)"  -o image/scanner/bin/scanner ./cmd/clair

#####################################################################
###### Binaries we depend on (need to be defined on top) ############
#####################################################################

STATICCHECK_BIN := $(GOPATH)/bin/staticcheck
$(STATICCHECK_BIN): deps
	@echo "+ $@"
	@go install honnef.co/go/tools/cmd/staticcheck

EASYJSON_BIN := $(GOPATH)/bin/easyjson
$(EASYJSON_BIN): deps
	@echo "+ $@"
	go install github.com/mailru/easyjson/easyjson

GOLANGCILINT_BIN := $(GOBIN)/golangci-lint
$(GOLANGCILINT_BIN): deps
	@echo "+ $@"
	go install github.com/golangci/golangci-lint/cmd/golangci-lint

#############
##  Tag  ##
#############

.PHONY: tag
tag:
	@echo $(TAG)

#############
##  Build  ##
#############
.PHONY: build-updater
build-updater: deps
	@echo "+ $@"
	go build -o ./bin/updater ./cmd/updater

###########
## Style ##
###########
.PHONY: style
style: blanks golangci-lint staticcheck no-large-files

.PHONY: staticcheck
staticcheck: $(STATICCHECK_BIN)
	@echo "+ $@"
	@$(BASE_DIR)/tools/staticcheck-wrap.sh ./...

.PHONY: no-large-files
no-large-files:
	@echo "+ $@"
	@$(BASE_DIR)/tools/large-git-files/find.sh

.PHONY: golangci-lint
golangci-lint: $(GOLANGCILINT_BIN) proto-generated-srcs
ifdef CI
	@echo '+ $@'
	@echo 'The environment indicates we are in CI; running linters in check mode.'
	@echo 'If this fails, run `make style`.'
	@echo "Running with no tags..."
	golangci-lint run
	@echo "Running with release tags..."
	@# We use --tests=false because some unit tests don't compile with release tags,
	@# since they use functions that we don't define in the release build. That's okay.
	golangci-lint run --build-tags "$(subst $(comma),$(space),$(RELEASE_GOTAGS))" --tests=false
else
	golangci-lint run --fix
	golangci-lint run --fix --build-tags "$(subst $(comma),$(space),$(RELEASE_GOTAGS))" --tests=false
endif

.PHONY: blanks
blanks:
	@echo "+ $@"
ifdef CI
	@echo $(FORMATTING_FILES) | xargs $(BASE_DIR)/tools/import_validate.py
else
	@echo $(FORMATTING_FILES) | xargs $(BASE_DIR)/tools/fix-blanks.sh
endif

.PHONY: dev
dev: install-dev-tools
	@echo "+ $@"

deps: proto-generated-srcs go.mod
	@echo "+ $@"
	@go mod tidy
ifdef CI
	@git diff --exit-code -- go.mod go.sum || { echo "go.mod/go.sum files were updated after running 'go mod tidy', run this command on your local machine and commit the results." ; exit 1 ; }
	go mod verify
endif
	@touch deps

.PHONY: clean-deps
clean-deps:
	@echo "+ $@"
	@rm -f deps

GET_DEVTOOLS_CMD := $(MAKE) -qp | sed -e '/^\# Not a target:$$/{ N; d; }' | egrep -v '^(\s*(\#.*)?$$|\s|%|\(|\.)' | egrep '^[^[:space:]:]*:' | cut -d: -f1 | sort | uniq | grep '^$(GOPATH)/bin/'
.PHONY: clean-dev-tools
clean-dev-tools:
	@echo "+ $@"
	@$(GET_DEVTOOLS_CMD) | xargs rm -fv

.PHONY: reinstall-dev-tools
reinstall-dev-tools: clean-dev-tools
	@echo "+ $@"
	@$(MAKE) install-dev-tools

.PHONY: install-dev-tools
install-dev-tools:
	@echo "+ $@"
	@$(GET_DEVTOOLS_CMD) | xargs $(MAKE)

############
## Images ##
############

.PHONY: all-images
all-images: image image-slim

.PHONY: image
image: scanner-image db-image

.PHONY: image-slim
image-slim: scanner-image-slim db-image-slim

.PHONY: scanner-image-builder
scanner-image-builder:
	@echo "+ $@"
	scripts/ensure_image.sh $(BUILD_IMAGE) build/Dockerfile build/

.PHONY: scanner-build-dockerized
scanner-build-dockerized: scanner-image-builder deps
	@echo "+ $@"
ifdef CI
	docker container create --name builder $(BUILD_IMAGE) $(BUILD_CMD)
	docker cp $(GOPATH) builder:/
	docker start -i builder
	docker cp builder:/go/src/github.com/stackrox/scanner/image/scanner/bin/scanner image/scanner/bin/scanner
else
	docker run $(BUILD_FLAGS) $(GOPATH_WD_OVERRIDES) $(LOCAL_VOLUME_ARGS) $(BUILD_IMAGE) $(BUILD_CMD)
endif

.PHONY: $(CURDIR)/image/scanner/rhel/bundle.tar.gz
$(CURDIR)/image/scanner/rhel/bundle.tar.gz: build
	$(CURDIR)/image/scanner/rhel/create-bundle.sh $(CURDIR)/image/scanner $(CURDIR)/image/scanner/rhel

.PHONY: $(CURDIR)/image/db/rhel/bundle.tar.gz
$(CURDIR)/image/db/rhel/bundle.tar.gz:
	$(CURDIR)/image/db/rhel/create-bundle.sh $(CURDIR)/image/db $(CURDIR)/image/db/rhel

.PHONY: scanner-image
scanner-image: scanner-build-dockerized ossls-notice $(CURDIR)/image/scanner/rhel/bundle.tar.gz
	@echo "+ $@"
	@docker build --target scanner -t us.gcr.io/stackrox-ci/scanner:$(TAG) -f image/scanner/rhel/Dockerfile image/scanner/rhel

.PHONY: scanner-image-slim
scanner-image-slim: scanner-build-dockerized ossls-notice $(CURDIR)/image/scanner/rhel/bundle.tar.gz
	@echo "+ $@"
	@docker build --target scanner-slim -t us.gcr.io/stackrox-ci/scanner-slim:$(TAG) -f image/scanner/rhel/Dockerfile image/scanner/rhel

.PHONY: db-image
db-image: $(CURDIR)/image/db/rhel/bundle.tar.gz
	@echo "+ $@"
	@test -f image/db/dump/definitions.sql.gz || { echo "FATAL: No definitions dump found in image/dump/definitions.sql.gz. Exiting..."; exit 1; }
	@docker build --target scanner-db -t us.gcr.io/stackrox-ci/scanner-db:$(TAG) -f image/db/rhel/Dockerfile image/db/rhel

.PHONY: db-image-slim
db-image-slim: $(CURDIR)/image/db/rhel/bundle.tar.gz
	@echo "+ $@"
	@test -f image/db/dump/definitions.sql.gz || { echo "FATAL: No definitions dump found in image/dump/definitions.sql.gz. Exiting..."; exit 1; }
	@docker build --target scanner-db-slim -t us.gcr.io/stackrox-ci/scanner-db-slim:$(TAG) -f image/db/rhel/Dockerfile image/db/rhel

.PHONY: deploy
deploy: clean-helm-rendered
	@echo "+ $@"
	kubectl create namespace stackrox || true
	helm template scanner chart/ --set tag=$(TAG),logLevel=$(LOGLEVEL),updateInterval=2m --output-dir rendered-chart
	kubectl apply -R -f rendered-chart

.PHONY: deploy-dockerhub
deploy-dockerhub: clean-helm-rendered
	@echo "+ $@"
	kubectl create namespace stackrox || true
	helm template scanner chart/ --set tag=$(TAG),logLevel=$(LOGLEVEL),updateInterval=2m,scannerImage=stackrox/scanner,scannerDBImage=stackrox/scanner-db --output-dir rendered-chart
	kubectl apply -R -f rendered-chart

.PHONY: ossls-notice
ossls-notice: deps
	ossls version
	ossls audit --export image/scanner/rhel/THIRD_PARTY_NOTICES

###########
## Tests ##
###########

.PHONY: unit-tests
unit-tests: deps
	@echo "+ $@"
	go test -race ./...

.PHONY: e2e-tests
e2e-tests: deps
	@echo "+ $@"
	go test -tags e2e -count=1 -timeout=20m ./e2etests/...

.PHONY: db-integration-tests
db-integration-tests: deps
	@echo "+ $@"
	go test -tags db_integration -count=1 ./database/pgsql

.PHONY: scale-tests
scale-tests: deps
	@echo "+ $@"
	mkdir /tmp/pprof
	go run ./scale/... /tmp/pprof || true
	zip -r /tmp/pprof.zip /tmp/pprof

####################
## Generated Srcs ##
####################

PROTO_GENERATED_SRCS = $(GENERATED_PB_SRCS) $(GENERATED_API_GW_SRCS)

include make/protogen.mk

.PHONY: clean-obsolete-protos
clean-obsolete-protos:
	@echo "+ $@"
	$(BASE_DIR)/tools/clean_autogen_protos.py --protos $(BASE_DIR)/proto --generated $(BASE_DIR)/generated

proto-generated-srcs: $(PROTO_GENERATED_SRCS)
	@echo "+ $@"
	@touch proto-generated-srcs
	@$(MAKE) clean-obsolete-protos

.PHONY: go-easyjson-srcs
go-easyjson-srcs: $(EASYJSON_BIN)
	@echo "+ $@"
	@easyjson -pkg pkg/vulnloader/nvdloader
	@easyjson -pkg api/v1

clean-proto-generated-srcs:
	@echo "+ $@"
	git clean -xdf generated

###########
## Clean ##
###########
.PHONY: clean
clean: clean-image clean-helm-rendered clean-proto-generated-srcs clean-pprof
	@echo "+ $@"

.PHONY: clean-image
clean-image:
	@echo "+ $@"
	git clean -xdf image/bin

.PHONY: clean-helm-rendered
clean-helm-rendered:
	@echo "+ $@"
	git clean -xdf rendered-chart

.PHONY: clean-pprof
clean-pprof:
	@echo "+ $@"
	rm /tmp/pprof.zip || true
	rm -rf /tmp/pprof
