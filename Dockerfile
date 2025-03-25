# Build the manager binary
FROM registry.ci.openshift.org/stolostron/builder:go1.23-linux AS builder

WORKDIR /workspace
# Copy the source files
COPY main.go main.go
COPY go.mod go.mod
COPY go.sum go.sum
COPY api/. api/
COPY config/. config/
COPY controllers/ controllers/

# Copy the go source
RUN  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go mod vendor
RUN  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go mod tidy

# Build
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -a -o manager main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
