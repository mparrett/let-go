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
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# The fully pinned lock, including transitive dependencies. Edit
# requirements.in and recompile; see the header in requirements.txt.
COPY requirements.txt .
# --no-deps because the lock is already transitively complete: pip installs
# exactly these versions and cannot quietly pull in anything else.
RUN pip install --no-cache-dir --no-deps -r requirements.txt

COPY backends.py runner.py ./

# Scratch space for the two clones. Nothing here is meant to outlive the run.
ENV WORKDIR=/work
RUN mkdir -p /work

# PYTHONUNBUFFERED so progress streams while the job runs; on an ephemeral
# machine, buffered output that never flushes is output that never existed.
ENV PYTHONUNBUFFERED=1

ENTRYPOINT ["python", "/app/runner.py"]
