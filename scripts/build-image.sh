#!/bin/bash

set -eo pipefail

docker_image_name="${1:-"malston/bosh-exporter"}"
docker_image_tag="${2:-"3.3.1"}"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build bosh_exporter.go
DOCKER_IMAGE_NAME=${docker_image_name} DOCKER_IMAGE_TAG=${docker_image_tag} make docker

docker login
docker tag "${docker_image_name}:${docker_image_tag}" "${docker_image_name}:${docker_image_tag}"
docker push "${docker_image_name}:${docker_image_tag}"
