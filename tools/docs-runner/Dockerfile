# ---------------------------------------------------------------------------
# attractor: the agentic backend's binary, built from a pinned commit.
# ---------------------------------------------------------------------------
# Only MODEL_BACKEND=attractor uses this. It is built here rather than pulled
# because the strange-lettractor image is not published anywhere this build can
# reach, and vendoring the source would put another project's tree in this
# repository. A pinned commit means the comparison is reproducible: a number
# from this image is attributable to one revision of the pipeline runtime.
#
# Mirrors strange-lettractor's own Dockerfile stage 1 -- the released lg and lgx
# rather than a source build, verified against the release checksums. Keep
# LETGO_VERSION equal to that commit's lgx.edn :lg-version; lgx refuses to run
# on a mismatch, so a skew fails the build rather than the deploy.
# `--platform` is load-bearing, not tidiness. `lgx build` bundles the project
# into a COPY of the lg executable that produced it, so bin/attractor inherits
# lg's architecture -- and lg is downloaded below as an explicit linux/amd64
# release. On an arm64 Mac an unpinned base would pull arm64 Debian, which then
# cannot execute the amd64 lg at all and the build fails here rather than on
# the droplet. Pinned by tag rather than digest, unlike the runtime stage: this
# stage contributes no bytes to the final image beyond one statically bundled
# binary, whose reproducibility comes from the pinned lg, lgx and source
# revisions below.
FROM --platform=linux/amd64 debian:bookworm-slim AS attractor
ARG LETTRACTOR_REPO=https://github.com/mparrett/strange-lettractor.git
# The tip of `container-build` as of 2026-09-20, which is the revision the
# container was built and smoke-tested against. A branch name here would make
# the image silently track later pushes, which an evaluation must not do.
ARG LETTRACTOR_SHA=12286f7581192d7a4db0bffe44092e5f97f99452
ARG LETGO_VERSION=1.13.0
ARG LGX_VERSION=0.3.1
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git \
    && rm -rf /var/lib/apt/lists/*
# Plain RUN rather than the `--mount=type=tmpfs` scratch strange-lettractor's
# own Dockerfile uses: that form needs BuildKit or buildah, and the deploy
# workflow builds with podman on a runner whose version is not pinned. The
# download is removed in the same layer, and this is a discarded build stage in
# any case, so the mount would buy nothing here.
RUN set -eu; \
    tarball="let-go_${LETGO_VERSION}_linux_amd64.tar.gz"; \
    base="https://github.com/nooga/let-go/releases/download/v${LETGO_VERSION}"; \
    mkdir -p /dl; cd /dl; \
    curl -fsSLO "${base}/${tarball}"; \
    curl -fsSLO "${base}/checksums.txt"; \
    grep " ${tarball}$" checksums.txt | sha256sum -c -; \
    tar -xzf "${tarball}"; \
    install -m 0755 "$(find . -maxdepth 2 -type f -name lg | head -1)" /usr/local/bin/lg; \
    cd /; rm -rf /dl
RUN set -eu; \
    tarball="lgx_${LGX_VERSION}_linux_amd64.tar.gz"; \
    base="https://github.com/abogoyavlensky/lgx/releases/download/v${LGX_VERSION}"; \
    mkdir -p /dl; cd /dl; \
    curl -fsSLO "${base}/${tarball}"; \
    curl -fsSLO "${base}/checksums.txt"; \
    grep " ${tarball}$" checksums.txt | sha256sum -c -; \
    tar -xzf "${tarball}"; \
    install -m 0755 "$(find . -maxdepth 2 -type f -name lgx | head -1)" /usr/local/bin/lgx; \
    cd /; rm -rf /dl
# Cloned and then checked out by SHA rather than fetched by SHA directly: a
# bare `git fetch <sha>` depends on the server allowing an arbitrary object in
# a want line, and failing on that would look like a network problem. The clone
# is how the objects arrive; the checkout is what pins the build.
WORKDIR /src
RUN set -eu; git clone -q "$LETTRACTOR_REPO" .; git checkout -q "$LETTRACTOR_SHA"
RUN lgx install && lgx build && test -x bin/attractor && bin/attractor help >/dev/null

# Pinned by digest, not by tag: `python:3.12-slim` is a moving target, so the
# same commit could otherwise build on a different interpreter tomorrow.
#
# This digest is the **linux/amd64** manifest, deliberately. Deployment targets
# are DigitalOcean droplets, which are x86_64; development happens on Apple
# Silicon. Pinning the architecture here means both build the same image rather
# than each building for its own host.
#
# Do NOT resolve this digest with `podman image inspect --format
# '{{index .RepoDigests 0}}'`. On an arm64 machine that returns the *arm64*
# manifest, which pins the image to arm64 everywhere and fails on a droplet
# with `exec container process /usr/local/bin/python: Exec format error`. That
# is exactly how this line was wrong before. Read the index instead and take
# the entry whose platform.architecture is amd64:
#
#   podman manifest inspect docker.io/library/python:3.12-slim
#
# Resolved 2026-09-09: python 3.12.14-slim-trixie, linux/amd64.
FROM python:3.12-slim@sha256:2fe5997d249a808b8eeea52c58a1dbffbba28754dc11699ef5c029f2d818ce79

# git is the job's main tool; ca-certificates is needed to reach api.github.com.
# apt versions are deliberately unpinned - Debian moves security fixes through
# these and the snapshot archive needed to pin them is more fragility than a
# git binary warrants.
# bash, openssl and procps are for MODEL_BACKEND=attractor only: strange-
# lettractor's own Dockerfile names them as what bin/attractor shells out to,
# its maker stages run commands through bash, and its process supervision uses
# pkill. Cheap, and a missing one costs a whole cluster run to discover.
#
# graphviz is deliberately NOT here, though that same list includes it: `dot`
# backs `attractor graph` and the server's graph route, and this image runs
# neither -- AttractorBackend only ever calls `attractor run`. Verified by
# running pipelines/page-update.dot end to end in an image without it
# (2026-09-21). It is worth naming because graphviz pulls the X11 client
# libraries into a job container that has no other use for them.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        git ca-certificates bash openssl procps \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# The fully pinned lock, including transitive dependencies. Edit
# requirements.in and recompile; see the header in requirements.txt.
COPY requirements.txt .
# --no-deps because the lock is already transitively complete: pip installs
# exactly these versions and cannot quietly pull in anything else.
RUN pip install --no-cache-dir --no-deps -r requirements.txt

COPY backends.py runner.py ./
# The DOT workflow AttractorBackend runs. Its default path is baked into that
# class; ATTRACTOR_GRAPH overrides it for a one-off experiment without a
# rebuild.
COPY pipelines/ ./pipelines/

# A let-go bundle: the lg executable with the project appended, so it carries
# no runtime dependency on the build stage.
COPY --from=attractor /src/bin/attractor /usr/local/bin/attractor

# Scratch space for the two clones. Nothing here is meant to outlive the run.
ENV WORKDIR=/work
# attractor writes run artifacts under ATTRACTOR_HOME. Pointing it into the
# scratch space keeps them with the clones, where nothing is meant to outlive
# the run, rather than in the image's root home directory.
ENV ATTRACTOR_HOME=/work/attractor-home
RUN mkdir -p /work /work/attractor-home

# PYTHONUNBUFFERED so progress streams while the job runs; on an ephemeral
# machine, buffered output that never flushes is output that never existed.
ENV PYTHONUNBUFFERED=1

ENTRYPOINT ["python", "/app/runner.py"]
