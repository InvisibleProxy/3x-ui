#!/bin/sh

PLATFORM=$1
VERSION=${2:-25.10.15}

case "$PLATFORM" in
    linux/amd64)
        ARCH="64"
        ;;
    linux/386)
        ARCH="32"
        ;;
    linux/arm64|linux/arm64/v8)
        ARCH="arm64-v8a"
        ;;
    linux/arm/v7)
        ARCH="arm32-v7a"
        ;;
    linux/arm/v6)
        ARCH="arm32-v6"
        ;;
    *)
        ARCH="64"
        ;;
esac

echo "Downloading Xray v${VERSION} for ${PLATFORM} (${ARCH})..."
wget -q "https://github.com/XTLS/Xray-core/releases/download/v${VERSION}/Xray-linux-${ARCH}.zip"
unzip -q "Xray-linux-${ARCH}.zip"
install -m 755 xray /usr/bin/xray
rm -f "Xray-linux-${ARCH}.zip" xray geoip.dat geosite.dat
echo "Xray binary installed: /usr/bin/xray"
