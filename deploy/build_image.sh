#!/usr/bin/env bash
# 本地构建镜像的快速脚本，避免在命令行反复输入构建参数。

set -euo pipefail

IMAGE_REPO="harbor.gdalpha.com/alpha-tools/sub2api"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
VERSION=`cat ${REPO_ROOT}/backend/cmd/server/VERSION`
AUTHOR='lsh'
IMAGE_NAME="${IMAGE_REPO}:${AUTHOR}.${VERSION}"
echo "SCRIPT_DIR: ${SCRIPT_DIR}"
echo "REPO_ROOT: ${REPO_ROOT}"
echo "VERSION: ${VERSION}"
echo "IMAGE_NAME: ${IMAGE_NAME}"
docker build -t ${IMAGE_NAME} \
    --build-arg GOPROXY=https://goproxy.cn,direct \
    --build-arg GOSUMDB=sum.golang.google.cn \
    --build-arg NODE_IMAGE=node:20-alpine \
    -f "${REPO_ROOT}/Dockerfile" \
    "${REPO_ROOT}"
docker push ${IMAGE_NAME}


