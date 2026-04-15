#!/bin/bash
set -euo pipefail

# Release script for STYLiTE Orbit Mattermost to Telegrambot.
# Usage: ./release.sh [major|minor|patch]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

BUMP_TYPE="${1:-patch}"

if [[ ! "$BUMP_TYPE" =~ ^(major|minor|patch)$ ]]; then
    echo "Error: Invalid bump type '$BUMP_TYPE'. Must be major, minor, or patch."
    exit 1
fi

if ! git diff-index --quiet HEAD --; then
    echo "Error: You have uncommitted changes. Please commit or stash them first."
    exit 1
fi

if [[ ! -f VERSION ]]; then
    echo "Error: VERSION file not found."
    exit 1
fi

CURRENT_VERSION=$(cat VERSION | tr -d '\n\r ')
if [[ ! "$CURRENT_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: Invalid version format in VERSION file: '$CURRENT_VERSION'"
    exit 1
fi

IFS='.' read -r -a VERSION_PARTS <<< "$CURRENT_VERSION"
MAJOR="${VERSION_PARTS[0]}"
MINOR="${VERSION_PARTS[1]}"
PATCH="${VERSION_PARTS[2]}"

case "$BUMP_TYPE" in
    major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
    minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
    patch) PATCH=$((PATCH + 1)) ;;
esac

NEW_VERSION="${MAJOR}.${MINOR}.${PATCH}"
TAG_NAME="${NEW_VERSION}"

echo "Current version: $CURRENT_VERSION"
echo "New version:     $NEW_VERSION"
echo "Bump type:       $BUMP_TYPE"
echo ""

if [[ ! -f CHANGELOG.md ]]; then
    echo "Error: CHANGELOG.md not found."
    exit 1
fi

TODAY=$(date +%Y-%m-%d)

if ! grep -q "^## \[Unreleased\]" CHANGELOG.md; then
    echo "Warning: [Unreleased] section not found in CHANGELOG.md — adding it."
    if [[ "$(uname)" == "Darwin" ]]; then
        sed -i '' '7a\
\
## [Unreleased]
' CHANGELOG.md
    else
        sed -i '7a\\n## [Unreleased]' CHANGELOG.md
    fi
fi

if [[ "$(uname)" == "Darwin" ]]; then
    sed -i '' "/^## \[Unreleased\]/a\\
\\
## [$NEW_VERSION] - $TODAY
" CHANGELOG.md
else
    sed -i "/^## \[Unreleased\]/a\\\n## [$NEW_VERSION] - $TODAY" CHANGELOG.md
fi

echo -n "$NEW_VERSION" > VERSION
echo "✓ Updated VERSION and CHANGELOG.md"
echo ""

echo "Version and CHANGELOG.md changes:"
git --no-pager diff VERSION CHANGELOG.md
echo ""

read -p "Proceed with commit, tag, and push? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted. Changes have been made to VERSION and CHANGELOG.md — review and commit manually if desired."
    exit 0
fi

git add VERSION CHANGELOG.md
git commit -m "chore: bump version to $NEW_VERSION"
git tag -a "$TAG_NAME" -m "Release $NEW_VERSION"

echo "✓ Committed"
echo "✓ Tagged $TAG_NAME"
echo ""

echo "Pushing to origin..."
git push origin HEAD
git push origin "$TAG_NAME"

echo "✓ Pushed branch and tag"
echo ""
echo "Docker images that will be built on the tag push:"
echo "  - docker.io/styliteag/mattermost2telegrambot:$NEW_VERSION"
echo "  - docker.io/styliteag/mattermost2telegrambot:latest"
echo "  - ghcr.io/styliteag/mattermost2telegrambot:$NEW_VERSION"
echo "  - ghcr.io/styliteag/mattermost2telegrambot:latest"
echo ""
echo "Release $NEW_VERSION triggered successfully."
