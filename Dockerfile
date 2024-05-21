# # Update the base image in Makefile when updating golang version. This has to
# # be pre-pulled in order to work on GCB.
# # ARG ARCH=arm64
# ARG ARCH=amd64
# FROM golang:1.22.2 as build
# WORKDIR /go/src/sigs.k8s.io/metrics-server
# COPY go.mod .
# COPY go.sum .
# RUN go mod download

# COPY pkg pkg
# COPY cmd cmd
# COPY Makefile Makefile

# # ARG ARCH=arm64
# ARG ARCH=amd64
# ARG GIT_COMMIT
# ARG GIT_TAG
# RUN make metrics-server

# FROM gcr.io/distroless/static:latest-$ARCH
# COPY --from=build /go/src/sigs.k8s.io/metrics-server/metrics-server /
# USER 65534
# ENTRYPOINT ["/metrics-server"]


ARG ARCH=amd64
FROM golang:1.22.3 as build

# WORKDIR /go/src/sigs.k8s.io/metrics-server
WORKDIR /
COPY go.mod .
COPY go.sum .
RUN go mod download

COPY pkg pkg
COPY cmd cmd
COPY Makefile Makefile

ARG ARCH=amd64
ARG GIT_COMMIT
ARG GIT_TAG
RUN make metrics-server

FROM gcr.io/distroless/static:latest-$ARCH
# COPY --from=build /go/src/sigs.k8s.io/metrics-server/metrics-server /
COPY --from=build /metrics-server /
USER 65534
ENTRYPOINT ["/metrics-server"]