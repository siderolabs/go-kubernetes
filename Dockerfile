# syntax = docker/dockerfile-upstream:1.21.0-labs

# THIS FILE WAS AUTOMATICALLY GENERATED, PLEASE DO NOT EDIT.
#
# Generated on 2026-02-19T12:35:26Z by kres 6458cfd.

ARG TOOLCHAIN=scratch

# cleaned up specs and compiled versions
FROM scratch AS generate

# runs markdownlint
FROM docker.io/oven/bun:1.3.9-alpine AS lint-markdown
WORKDIR /src
RUN bun i markdownlint-cli@0.47.0 sentences-per-line@0.5.1
COPY .markdownlint.json .
COPY ./README.md ./README.md
RUN bunx markdownlint --ignore "CHANGELOG.md" --ignore "**/node_modules/**" --ignore '**/hack/chglog/**' --rules markdownlint-sentences-per-line .

# base toolchain image
FROM --platform=${BUILDPLATFORM} ${TOOLCHAIN} AS toolchain
RUN apk --update --no-cache add bash build-base curl jq protoc protobuf-dev

# build tools
FROM --platform=${BUILDPLATFORM} toolchain AS tools
ENV GO111MODULE=on
ARG CGO_ENABLED
ENV CGO_ENABLED=${CGO_ENABLED}
ARG GOTOOLCHAIN
ENV GOTOOLCHAIN=${GOTOOLCHAIN}
ARG GOEXPERIMENT
ENV GOEXPERIMENT=${GOEXPERIMENT}
ENV GOPATH=/go
ARG DEEPCOPY_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg go install github.com/siderolabs/deep-copy@${DEEPCOPY_VERSION} \
	&& mv /go/bin/deep-copy /bin/deep-copy
ARG GOLANGCILINT_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCILINT_VERSION} \
	&& mv /go/bin/golangci-lint /bin/golangci-lint
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg go install golang.org/x/vuln/cmd/govulncheck@latest \
	&& mv /go/bin/govulncheck /bin/govulncheck
ARG GOFUMPT_VERSION
RUN go install mvdan.cc/gofumpt@${GOFUMPT_VERSION} \
	&& mv /go/bin/gofumpt /bin/gofumpt

# tools and sources
FROM tools AS base
WORKDIR /src
COPY go.mod go.mod
COPY go.sum go.sum
RUN cd .
RUN --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg go mod download
RUN --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg go mod verify
COPY ./internal ./internal
COPY ./kubernetes ./kubernetes
RUN --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg go list -mod=readonly all >/dev/null

# builds integration-test-darwin-amd64
FROM base AS integration-test-darwin-amd64-build
COPY --from=generate / /
WORKDIR /src/internal/integration
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg GOARCH=amd64 GOOS=darwin go test -c -covermode=atomic -coverpkg=github.com/siderolabs/go-kubernetes/... -tags integration,sidero.debug -ldflags "${GO_LDFLAGS}" -o /integration-test-darwin-amd64

# builds integration-test-darwin-arm64
FROM base AS integration-test-darwin-arm64-build
COPY --from=generate / /
WORKDIR /src/internal/integration
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg GOARCH=arm64 GOOS=darwin go test -c -covermode=atomic -coverpkg=github.com/siderolabs/go-kubernetes/... -tags integration,sidero.debug -ldflags "${GO_LDFLAGS}" -o /integration-test-darwin-arm64

# builds integration-test-linux-amd64
FROM base AS integration-test-linux-amd64-build
COPY --from=generate / /
WORKDIR /src/internal/integration
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg GOARCH=amd64 GOOS=linux go test -c -covermode=atomic -coverpkg=github.com/siderolabs/go-kubernetes/... -tags integration,sidero.debug -ldflags "${GO_LDFLAGS}" -o /integration-test-linux-amd64

# builds integration-test-linux-arm64
FROM base AS integration-test-linux-arm64-build
COPY --from=generate / /
WORKDIR /src/internal/integration
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg GOARCH=arm64 GOOS=linux go test -c -covermode=atomic -coverpkg=github.com/siderolabs/go-kubernetes/... -tags integration,sidero.debug -ldflags "${GO_LDFLAGS}" -o /integration-test-linux-arm64

# runs gofumpt
FROM base AS lint-gofumpt
RUN FILES="$(gofumpt -l .)" && test -z "${FILES}" || (echo -e "Source code is not formatted with 'gofumpt -w .':\n${FILES}"; exit 1)

# runs golangci-lint
FROM base AS lint-golangci-lint
WORKDIR /src
COPY .golangci.yml .
ENV GOGC=50
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/root/.cache/golangci-lint,id=go-kubernetes/root/.cache/golangci-lint,sharing=locked --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg golangci-lint run --config .golangci.yml

# runs golangci-lint fmt
FROM base AS lint-golangci-lint-fmt-run
WORKDIR /src
COPY .golangci.yml .
ENV GOGC=50
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/root/.cache/golangci-lint,id=go-kubernetes/root/.cache/golangci-lint,sharing=locked --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg golangci-lint fmt --config .golangci.yml
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/root/.cache/golangci-lint,id=go-kubernetes/root/.cache/golangci-lint,sharing=locked --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg golangci-lint run --fix --issues-exit-code 0 --config .golangci.yml

# runs govulncheck
FROM base AS lint-govulncheck
WORKDIR /src
COPY --chmod=0755 hack/govulncheck.sh ./hack/govulncheck.sh
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg ./hack/govulncheck.sh ./...

# runs unit-tests with race detector
FROM base AS unit-tests-race
WORKDIR /src
ARG TESTPKGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg --mount=type=cache,target=/tmp,id=go-kubernetes/tmp CGO_ENABLED=1 go test -race ${TESTPKGS}

# runs unit-tests
FROM base AS unit-tests-run
WORKDIR /src
ARG TESTPKGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-kubernetes/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=go-kubernetes/go/pkg --mount=type=cache,target=/tmp,id=go-kubernetes/tmp go test -covermode=atomic -coverprofile=coverage.txt -coverpkg=${TESTPKGS} ${TESTPKGS}

FROM scratch AS integration-test-darwin-amd64
COPY --from=integration-test-darwin-amd64-build /integration-test-darwin-amd64 /integration-test-darwin-amd64

FROM scratch AS integration-test-darwin-arm64
COPY --from=integration-test-darwin-arm64-build /integration-test-darwin-arm64 /integration-test-darwin-arm64

FROM scratch AS integration-test-linux-amd64
COPY --from=integration-test-linux-amd64-build /integration-test-linux-amd64 /integration-test-linux-amd64

FROM scratch AS integration-test-linux-arm64
COPY --from=integration-test-linux-arm64-build /integration-test-linux-arm64 /integration-test-linux-arm64

# clean golangci-lint fmt output
FROM scratch AS lint-golangci-lint-fmt
COPY --from=lint-golangci-lint-fmt-run /src .

FROM scratch AS unit-tests
COPY --from=unit-tests-run /src/coverage.txt /coverage-unit-tests.txt

FROM integration-test-linux-${TARGETARCH} AS integration-test

FROM scratch AS integration-test-all
COPY --from=integration-test-darwin-amd64 / /
COPY --from=integration-test-darwin-arm64 / /
COPY --from=integration-test-linux-amd64 / /
COPY --from=integration-test-linux-arm64 / /

