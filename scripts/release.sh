#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [[ $# -ne 1 ]]; then
  echo "usage: $0 patch|minor|major" >&2
  exit 2
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "working tree is not clean; commit or stash changes before releasing" >&2
  exit 1
fi

current="$(tr -d '[:space:]' < VERSION)"
IFS=. read -r major minor patch <<< "$current"
case "$1" in
  patch) patch=$((patch + 1)) ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  major) major=$((major + 1)); minor=0; patch=0 ;;
  *)
    echo "usage: $0 patch|minor|major" >&2
    exit 2
    ;;
esac

next="${major}.${minor}.${patch}"
tag="v${next}"
if git rev-parse --verify --quiet "refs/tags/$tag" >/dev/null; then
  echo "tag already exists: $tag" >&2
  exit 1
fi

printf '%s\n' "$next" > VERSION

if command -v git-cliff >/dev/null 2>&1; then
  git cliff --unreleased --tag "$tag" --prepend CHANGELOG.md
else
  echo "warning: git-cliff is not installed; update CHANGELOG.md manually before committing." >&2
fi

make check

git add VERSION CHANGELOG.md
git commit -m "chore(release): ${tag}"
git tag -a "$tag" -m "Tack ${tag}"

echo
echo "Release ${tag} prepared locally. Nothing was pushed."
echo "After review:"
echo "  git push origin HEAD"
echo "  git push origin ${tag}"
