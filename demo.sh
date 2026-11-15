#!/usr/bin/env bash
# One command to see Gjallarhorn.
#
#   ./demo.sh            build the images the first time (10-15 minutes), then start the demo
#   ./demo.sh stop       stop it
#   ./demo.sh lab        run the attack lab and the co-pilot fire drill
#   ./demo.sh measure    measure lockdown speed and per-call cost
#   ./demo.sh rebuild    rebuild the images, then start the demo
#
# Works with Podman or Docker (set ENGINE=docker to choose), on Linux, macOS and Windows
# (Git Bash). No internet access is needed once the images are built.
set -euo pipefail
export MSYS_NO_PATHCONV=1
cd "$(dirname "$0")"

if [ -z "${ENGINE:-}" ]; then
  if command -v podman >/dev/null 2>&1; then ENGINE=podman
  elif command -v docker >/dev/null 2>&1; then ENGINE=docker
  else
    echo "Gjallarhorn's demo runs in a container. Install Podman (https://podman.io) or Docker first." >&2
    exit 1
  fi
fi
export ENGINE

if ! $ENGINE info >/dev/null 2>&1; then
  echo "Cannot reach $ENGINE." >&2
  [ "$ENGINE" = podman ] && echo "On Windows or macOS, start it with: podman machine start" >&2
  [ "$ENGINE" = docker ] && echo "Start Docker Desktop (or the Docker service) and try again." >&2
  exit 1
fi

# Podman names local images localhost/<name>; Docker uses the bare name.
base="gjallarhorn-bifrost:latest"
[ "$ENGINE" = podman ] && base="localhost/gjallarhorn-bifrost:latest"

have() { $ENGINE image inspect "$1" >/dev/null 2>&1; }

build() {
  echo "Building Bifrost with the Gjallarhorn plugin. The first build takes 10-15 minutes."
  $ENGINE build -t gjallarhorn-bifrost -f deploy/Containerfile .
  echo "Building the demo (agents, tools, console, attack lab)..."
  $ENGINE build -t gjallarhorn-demo --build-arg BASE="$base" -f deploy/demo/Containerfile .
}

ensure() { { have gjallarhorn-bifrost && have gjallarhorn-demo; } || build; }

case "${1:-start}" in
  start) ensure; exec bash deploy/demo/run-console.sh ;;
  stop) exec bash deploy/demo/run-console.sh stop ;;
  lab) ensure; exec bash deploy/demo/run-lab.sh ;;
  measure) ensure; exec bash deploy/demo/run-measure.sh ;;
  rebuild) build; exec bash deploy/demo/run-console.sh ;;
  -h | --help | help) sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) echo "unknown command: $1 (try ./demo.sh help)" >&2; exit 2 ;;
esac
