#!/usr/bin/env bash
# Repoint the Go module path after forking this repo.
#
#   ./hack/rename-module.sh github.com/your-org/your-repo
#
set -euo pipefail

NEW_MODULE="${1:-}"
OLD_MODULE="github.com/igofman/gke-mixed-generation-storage"

if [[ -z "${NEW_MODULE}" ]]; then
  echo "usage: $0 NEW_MODULE_PATH" >&2
  echo "example: $0 github.com/your-org/gke-mixed-generation-storage" >&2
  exit 2
fi

if [[ "${NEW_MODULE}" == "${OLD_MODULE}" ]]; then
  echo "module path is already ${NEW_MODULE}, nothing to do"
  exit 0
fi

cd "$(dirname "$0")/.."

echo "renaming ${OLD_MODULE} -> ${NEW_MODULE}"

# Go sources and go.mod.
while IFS= read -r -d '' file; do
  if grep -q "${OLD_MODULE}" "${file}"; then
    # Portable in-place edit: BSD sed and GNU sed disagree about -i.
    tmp="$(mktemp)"
    sed "s|${OLD_MODULE}|${NEW_MODULE}|g" "${file}" >"${tmp}"
    mv "${tmp}" "${file}"
    echo "  ${file}"
  fi
done < <(find . -type f \( -name '*.go' -o -name 'go.mod' \) -not -path './vendor/*' -print0)

gofmt -w .
go mod tidy

echo
echo "done. Docs, the Helm chart and the container image reference still point"
echo "at the original repo - update these by hand if you are publishing:"
grep -rl "igofman" --include='*.md' --include='*.yaml' --include='*.yml' . 2>/dev/null | sed 's/^/  /' || true
