#!/bin/sh
set -eu

repository="jaydip216/Rowlight"
install_dir=${ROWLIGHT_INSTALL_DIR:-/usr/local/bin}
release_base=${ROWLIGHT_RELEASE_BASE:-"https://github.com/$repository/releases/download"}
tag=${ROWLIGHT_VERSION:-}

if [ "$(uname -s)" != "Darwin" ]; then
  echo "Rowlight's curl installer currently supports macOS only." >&2
  exit 1
fi

case "$(uname -m)" in
  arm64) architecture=arm64 ;;
  x86_64) architecture=amd64 ;;
  *)
    echo "Unsupported Mac architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

for command in curl tar shasum; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required command not found: $command" >&2
    exit 1
  fi
done

if [ -z "$tag" ]; then
  release_metadata=$(curl -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/$repository/releases/latest" 2>/dev/null \
    || curl -fsSL \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/$repository/releases?per_page=1")
  tag=$(printf '%s\n' "$release_metadata" \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n 1)
  if [ -z "$tag" ]; then
    echo "Could not determine the latest Rowlight release." >&2
    exit 1
  fi
fi

version=${tag#v}
archive="rowlight-${version}-darwin-${architecture}.tar.gz"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/rowlight-install.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT INT TERM

echo "Downloading Rowlight $version for macOS $architecture..."
curl -fsSL "$release_base/$tag/$archive" -o "$temporary_dir/$archive"
curl -fsSL "$release_base/$tag/SHA256SUMS" -o "$temporary_dir/SHA256SUMS"

expected=$(awk -v archive="$archive" '$2 == archive { print $1 }' "$temporary_dir/SHA256SUMS")
if [ -z "$expected" ]; then
  echo "The release checksum file does not contain $archive." >&2
  exit 1
fi
actual=$(shasum -a 256 "$temporary_dir/$archive" | awk '{ print $1 }')
if [ "$actual" != "$expected" ]; then
  echo "Checksum verification failed for $archive." >&2
  exit 1
fi

tar -xzf "$temporary_dir/$archive" -C "$temporary_dir" rowlight

if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
  install -m 0755 "$temporary_dir/rowlight" "$install_dir/rowlight"
elif [ "$(id -u)" -eq 0 ]; then
  mkdir -p "$install_dir"
  install -m 0755 "$temporary_dir/rowlight" "$install_dir/rowlight"
elif command -v sudo >/dev/null 2>&1; then
  sudo mkdir -p "$install_dir"
  sudo install -m 0755 "$temporary_dir/rowlight" "$install_dir/rowlight"
else
  echo "Cannot write to $install_dir and sudo is unavailable." >&2
  echo "Set ROWLIGHT_INSTALL_DIR to a writable directory and try again." >&2
  exit 1
fi

echo "Installed Rowlight $version to $install_dir/rowlight"
echo "Run: rowlight"
