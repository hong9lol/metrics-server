#!/bin/bash

# 스크립트 오류가 발생할 경우 중지
set -e

# Go 설치 확인
if ! command -v go &> /dev/null; then
    echo "Go가 설치되어 있지 않습니다. Homebrew를 통해 설치하세요: brew install go"
    exit 1
fi

# Docker 설치 확인
if ! command -v docker &> /dev/null; then
    echo "Docker가 설치되어 있지 않습니다. Docker를 설치하고 다시 시도하세요."
    exit 1
fi

# Buildx 설치 및 활성화 확인
if ! docker buildx version &> /dev/null; then
    echo "Docker Buildx가 설치되어 있지 않습니다. Docker를 최신 버전으로 업그레이드하세요."
    exit 1
fi

# Go 및 ARCH 환경 변수 설정
export GOARCH=arm64
export GOOS=linux
export ARCH=arm64

# metrics-server 저장소 클론
#if [ -d "metrics-server" ]; then
#    echo "metrics-server 디렉토리가 이미 존재합니다. 기존 디렉토리를 사용합니다#."
#else
#    git clone https://github.com/kubernetes-sigs/metrics-server.git
#fi

# 빌드 디렉토리로 이동
#cd metrics-server

# 바이너리 빌드
echo "metrics-server 바이너리를 빌드합니다..."
make build

# Docker 빌드x 활성화
echo "Docker Buildx를 활성화합니다..."
docker buildx create --use || true

# Docker 이미지 생성
echo "Docker 이미지를 빌드합니다..."
pwd
docker buildx build --load --platform linux/${ARCH} -t custom-metrics-server:v0.0.1  .

echo "모든 과정이 완료되었습니다. 'custom-metrics-server:latest' 이미지를 사용하여 배포하세요."

