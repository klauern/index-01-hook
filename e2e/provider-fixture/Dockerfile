FROM docker.io/library/golang:1.26.11@sha256:4165ee6bf3b7811fd7b40373290405c83d0167dcb87ee5f29c427cdcef45ff8f AS build

WORKDIR /src
COPY main.go .
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/provider-fixture main.go

FROM scratch
COPY --from=build /out/provider-fixture /provider-fixture
USER 65532:65532
EXPOSE 443
ENTRYPOINT ["/provider-fixture"]
