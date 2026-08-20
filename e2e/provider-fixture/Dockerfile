FROM docker.io/library/golang:1.26.6@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6 AS build

WORKDIR /src
COPY main.go .
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/provider-fixture main.go

FROM scratch
COPY --from=build /out/provider-fixture /provider-fixture
USER 65532:65532
EXPOSE 443
ENTRYPOINT ["/provider-fixture"]
