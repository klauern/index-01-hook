FROM docker.io/library/golang:1.26.9@sha256:37cfcca63222ec499bec5e8ad7aad52dbf7529db36d5812b8eae2f0621aa92d6 AS build

WORKDIR /src
COPY main.go .
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/provider-fixture main.go

FROM scratch
COPY --from=build /out/provider-fixture /provider-fixture
USER 65532:65532
EXPOSE 443
ENTRYPOINT ["/provider-fixture"]
