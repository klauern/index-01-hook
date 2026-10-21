# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
# Local builds use the approved digest-pinned public Go image. Release tasks
# can override this value with another reviewed immutable builder image.
ARG GO_IMAGE=docker.io/library/golang:1.26.10@sha256:3745f1aa11822297b79113f5e84ce2bffeb039734571c39855809cfe9e5d7be6
FROM ${GO_IMAGE} AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=v0.0.0-dev
ARG COMMIT=dev
ARG BUILD_DATE=1970-01-01T00:00:00Z
RUN mkdir -p /out/data && \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/index-01-hook . && \
    go run ./scripts/release-notices \
      --version "${VERSION}" \
      --target "${TARGETOS}/${TARGETARCH}" \
      --output /out/THIRD_PARTY_NOTICES.txt

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /usr/local/go/lib/time/zoneinfo.zip
COPY --from=build /out/index-01-hook /index-01-hook
COPY --from=build /out/THIRD_PARTY_NOTICES.txt /usr/share/licenses/index-01-hook/THIRD_PARTY_NOTICES.txt
COPY --from=build /usr/share/doc/ca-certificates/copyright /usr/share/licenses/ca-certificates/copyright
COPY --from=build /usr/local/go/LICENSE /usr/share/licenses/go/LICENSE
COPY --from=build /usr/local/go/lib/time/README /usr/share/licenses/tzdata/README
COPY --from=build --chown=65532:65532 /out/data /var/lib/index-01-hook/data

ENV ZONEINFO=/usr/local/go/lib/time/zoneinfo.zip
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/index-01-hook"]
