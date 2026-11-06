#!/usr/bin/env bash
# Prepares a fresh Ubuntu 24.04 EC2 host: sudo scripts/host-setup.sh sut|loadgen
# On the sut host, Docker's data goes on the local NVMe instance store, so both Postgres and TigerBeetle
# fsync to it.
set -euo pipefail
role=$1
apt-get update -q
case $role in
sut)
  dev=$(lsblk -dpno NAME,MODEL | awk '/Instance Storage/ {print $1; exit}')
  [ -n "$dev" ] || { echo "no NVMe instance store found" >&2; exit 1; }
  mkfs.ext4 -F "$dev"
  mkdir -p /var/lib/docker
  mount -o noatime "$dev" /var/lib/docker
  apt-get install -yq docker.io docker-compose-v2 make curl
  usermod -aG docker ubuntu
  ;;
loadgen)
  # GOTOOLCHAIN=auto (the default) fetches the Go version go.mod asks for
  apt-get install -yq golang-go jq make
  ;;
*)
  echo "usage: host-setup.sh sut|loadgen" >&2
  exit 2
  ;;
esac
