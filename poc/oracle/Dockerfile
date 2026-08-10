FROM golang:1.26.5-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/oracle ./cmd/oracle

FROM debian:bookworm-slim

RUN groupadd --gid 65532 oracle \
    && useradd --uid 65532 --gid 65532 --no-create-home --shell /usr/sbin/nologin oracle
COPY --from=build /out/oracle /usr/local/bin/oracle
COPY --from=build /src/testdata/fixtures /usr/share/cnpatroni-oracle/fixtures
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/oracle"]
