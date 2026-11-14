FROM docker.io/library/golang:1.26.11@sha256:957952ae74dc010608717144399a59a2c6e82ea03d63be1c7ff6577c5a135f93 AS build

WORKDIR /src
COPY main.go .
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/provider-fixture main.go

FROM scratch
COPY --from=build /out/provider-fixture /provider-fixture
USER 65532:65532
EXPOSE 443
ENTRYPOINT ["/provider-fixture"]
