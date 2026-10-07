# MinIO server and client built from pinned upstream tags. MinIO no longer
# publishes community images or binaries, so the lab builds its own.
# See docs/adr/0003-minio-built-from-source.md.
FROM golang:1.23-bookworm AS build

ARG MINIO_RELEASE=RELEASE.2024-10-13T13-34-11Z
ARG MINIO_COMMIT=d10bb7e1b667c2df72c394ef1fa52ab4a6802d0f
ARG MC_RELEASE=RELEASE.2024-10-08T09-37-26Z
ARG MC_COMMIT=cf128de2cf42e763e7bd30c6df8b749fa94e0c10

ENV CGO_ENABLED=0
SHELL ["/bin/bash", "-eo", "pipefail", "-c"]

# Each tag is checked against its commit, so a re-pointed tag fails the build.
# Version strings follow upstream's buildscripts/gen-ldflags.go.
RUN git clone -q --depth 1 --branch "${MINIO_RELEASE}" https://github.com/minio/minio /src/minio \
 && test "$(git -C /src/minio rev-parse HEAD)" = "${MINIO_COMMIT}" \
 && cd /src/minio \
 && v="$(echo "${MINIO_RELEASE#RELEASE.}" | sed -E 's/T([0-9]{2})-([0-9]{2})-([0-9]{2})Z/T\1:\2:\3Z/')" \
 && go build -trimpath -o /out/minio -ldflags "-s -w \
      -X github.com/minio/minio/cmd.Version=${v} \
      -X github.com/minio/minio/cmd.ReleaseTag=${MINIO_RELEASE} \
      -X github.com/minio/minio/cmd.CommitID=${MINIO_COMMIT} \
      -X github.com/minio/minio/cmd.ShortCommitID=${MINIO_COMMIT:0:12} \
      -X github.com/minio/minio/cmd.CopyrightYear=${v:0:4}" .

RUN git clone -q --depth 1 --branch "${MC_RELEASE}" https://github.com/minio/mc /src/mc \
 && test "$(git -C /src/mc rev-parse HEAD)" = "${MC_COMMIT}" \
 && cd /src/mc \
 && v="$(echo "${MC_RELEASE#RELEASE.}" | sed -E 's/T([0-9]{2})-([0-9]{2})-([0-9]{2})Z/T\1:\2:\3Z/')" \
 && go build -trimpath -o /out/mc -ldflags "-s -w \
      -X github.com/minio/mc/cmd.Version=${v} \
      -X github.com/minio/mc/cmd.ReleaseTag=${MC_RELEASE} \
      -X github.com/minio/mc/cmd.CommitID=${MC_COMMIT} \
      -X github.com/minio/mc/cmd.ShortCommitID=${MC_COMMIT:0:12} \
      -X github.com/minio/mc/cmd.CopyrightYear=${v:0:4}" .

FROM debian:12-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && apt-get clean && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/minio /out/mc /usr/local/bin/
EXPOSE 9000 9001
ENTRYPOINT ["minio"]
CMD ["server", "/data"]
