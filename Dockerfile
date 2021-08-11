# Build the manager binary
FROM registry.ci.openshift.org/open-cluster-management/builder:go1.16-linux as builder

WORKDIR /workspace
# Copy the source files
COPY . .

# Copy the go source
RUN  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go mod vendor
RUN  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go mod tidy

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
