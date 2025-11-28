# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

BUILD_DATE?=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_VERSION?=$(shell git describe --tags --dirty 2>/dev/null)
GIT_COMMIT?=$(shell git rev-parse HEAD 2>/dev/null)
GIT_BRANCH?=$(shell git symbolic-ref --short HEAD 2>/dev/null)

BIN_DIR = ${PWD}/bin
ifeq (${GIT_VERSION},)
	GIT_VERSION=${GIT_BRANCH}
endif

# semver version
VERSION?=$(shell echo "${GIT_VERSION}" | sed -e 's/^v//')

IMAGE_REGISTRY?=docker.io
IMAGE_TAG=${GIT_VERSION}
ifeq (${IMAGE_TAG},main)
   IMAGE_TAG = latest
endif
# Image URL to use all building/pushing image targets
IMAGE ?=  ${IMAGE_REGISTRY}/xiaoshiai/installer:$(IMAGE_TAG)

GOPACKAGE=$(shell go list -m)
ldflags+=-w -s
ldflags+=-X '${GOPACKAGE}/pkg/version.gitVersion=${GIT_VERSION}'
ldflags+=-X '${GOPACKAGE}/pkg/version.gitCommit=${GIT_COMMIT}'
ldflags+=-X '${GOPACKAGE}/pkg/version.buildDate=${BUILD_DATE}'


.PHONY: all
all: build-binaries

.PHONY: generate
generate: generate-code

generate-code:
	$(CONTROLLER_GEN) paths="./apis/..." crd  output:crd:artifacts:config=deploy/installer/crds
	$(CONTROLLER_GEN) paths="./apis/..." object:headerFile="hack/boilerplate.go.txt"

add-license:
	addlicense  -l apache -c "The xiaoshiai Authors"

build: build-binaries

build-binaries:
	- mkdir -p ${BIN_DIR}
	CGO_ENABLED=0 go build -o ${BIN_DIR}/ -gcflags=all="-N -l" -ldflags="${ldflags}" ${GOPACKAGE}/cmd/...

test: 
	go test ./... -coverprofile=cover.out -covermode=atomic
	go tool cover -func=cover.out | grep total: | awk '{print "Total Coverage: " $$3}'

.PHONY: install.yaml
install.yaml:
	helm template installer --include-crds --namespace installer deploy/installer > install.yaml

release-image: build-binaries
	docker buildx build --platform linux/amd64,linux/arm64 -t ${IMAGE} --push .

CONTROLLER_GEN = ${BIN_DIR}/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	GOBIN=${BIN_DIR} go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
