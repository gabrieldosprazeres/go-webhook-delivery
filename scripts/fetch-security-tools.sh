#!/bin/sh
set -eu
umask 077

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 OUTPUT_DIRECTORY [syft,trivy,gitleaks]" >&2
  exit 64
fi

output=$1
selection=${2:-syft,trivy,gitleaks}
case "$output" in
  /*) ;;
  *) echo "OUTPUT_DIRECTORY must be absolute" >&2; exit 64 ;;
esac
mkdir -p "$output"
chmod 700 "$output"
manifest="$output/tools.tsv"
printf 'tool\tversion\tasset\tchecksum_expected\tchecksum_observed\n' >"$manifest"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os/$arch" in
  darwin/arm64)
    syft_asset=syft_1.52.0_darwin_arm64.tar.gz
    syft_sha=014d561b6d13059124155f74a6c5a9a99501f5e209313638dd884f39eb418ee6
    trivy_asset=trivy_0.74.0_macOS-ARM64.tar.gz
    trivy_sha=1caada5e0e2091909357c7525d3aa76f4b660b13821bc143b190c7483e31cc11
    gitleaks_asset=gitleaks_8.30.1_darwin_arm64.tar.gz
    gitleaks_sha=b40ab0ae55c505963e365f271a8d3846efbc170aa17f2607f13df610a9aeb6a5
    ;;
  linux/x86_64)
    syft_asset=syft_1.52.0_linux_amd64.tar.gz
    syft_sha=caeedb81fb0491615f1ebd1761e4145d41ee86dd2cc7bf80669f9f5ad9d6133d
    trivy_asset=trivy_0.74.0_Linux-64bit.tar.gz
    trivy_sha=2ae6fe3ee734b7fdf11335663e18c75ea12dccc76062f09f164a3b0f8be4371a
    gitleaks_asset=gitleaks_8.30.1_linux_x64.tar.gz
    gitleaks_sha=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb
    ;;
  *) echo "unsupported security-tool platform: $os/$arch" >&2; exit 65 ;;
esac

fetch() {
  project=$1 version=$2 asset=$3 expected=$4 binary=$5
  archive="$output/$asset"
  url="https://github.com/$project/releases/download/v$version/$asset"
  curl --fail --location --silent --show-error --retry 3 \
    --connect-timeout 10 --max-time 180 --output "$archive" "$url"
  actual=$(shasum -a 256 "$archive" | awk '{print $1}')
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $asset" >&2
    rm -f "$archive"
    exit 66
  fi
	printf '%s\t%s\t%s\t%s\t%s\n' "$binary" "$version" "$asset" "$expected" "$actual" >>"$manifest"
  tar -xzf "$archive" -C "$output" "$binary"
  chmod 500 "$output/$binary"
  rm -f "$archive"
}

case ",$selection," in *,syft,*) fetch anchore/syft 1.52.0 "$syft_asset" "$syft_sha" syft ;; esac
case ",$selection," in *,trivy,*) fetch aquasecurity/trivy 0.74.0 "$trivy_asset" "$trivy_sha" trivy ;; esac
case ",$selection," in *,gitleaks,*) fetch gitleaks/gitleaks 8.30.1 "$gitleaks_asset" "$gitleaks_sha" gitleaks ;; esac
chmod 600 "$manifest"
