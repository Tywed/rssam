#!/usr/bin/env bash
# One-shot GitHub release: commit current tree, tag, push (CI builds the tarball).
# Usage:  make release VERSION=0.1.4
#     or: ./scripts/release.sh 0.1.4
set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:-${VERSION:-}}"
VER="${VER#v}"
if [[ ! "$VER" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 X.Y.Z   (example: 0.1.4)" >&2
  exit 2
fi
TAG="v${VER}"

if [[ -n "$(git status --porcelain -- .env)" ]]; then
  echo "refusing to release: .env has local changes" >&2
  exit 1
fi

if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "refusing to release: tag $TAG already exists" >&2
  exit 1
fi

python3 - "$VER" <<'PY'
import pathlib, re, sys
ver = sys.argv[1]
path = pathlib.Path("changelog.md")
text = path.read_text()
if re.search(rf"^## {re.escape(ver)}$", text, re.M):
    sys.exit(0)
if "## Unreleased\n" not in text:
    sys.exit("changelog.md: missing ## Unreleased")
path.write_text(text.replace("## Unreleased\n", f"## Unreleased\n\n## {ver}\n", 1))
PY

git add -A
git add changelog.md scripts/release.sh Makefile

if git diff --cached --quiet; then
  echo "nothing to commit; creating tag $TAG on HEAD" >&2
else
  git commit -m "$(cat <<EOF
Release ${VER}: production fixes (bootstrap, WebSocket, retention, trusted proxies).

EOF
)"
fi

git tag -a "$TAG" -m "Release ${VER}"
git push origin HEAD
git push origin "$TAG"
echo "pushed $(git rev-parse --abbrev-ref HEAD) and $TAG"
