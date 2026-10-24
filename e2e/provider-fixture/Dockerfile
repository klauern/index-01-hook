FROM docker.io/library/golang:1.26.10@sha256:3745f1aa11822297b79113f5e84ce2bffeb039734571c39855809cfe9e5d7be6 AS build

WORKDIR /src
COPY main.go .
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/provider-fixture main.go

FROM scratch
COPY --from=build /out/provider-fixture /provider-fixture
USER 65532:65532
EXPOSE 443
ENTRYPOINT ["/provider-fixture"]
