#!/bin/bash
# Fast local build script for ARM (Raspberry Pi)
# This builds natively on ARM without cross-compilation (much faster than GitHub Actions)

set -e

echo "Building taro Docker image locally..."
docker build -t double2and9/taro:latest .

echo ""
echo "Build complete! To deploy:"
echo "  docker-compose restart taro"
echo ""
echo "To push to Docker Hub (optional):"
echo "  docker push double2and9/taro:latest"
