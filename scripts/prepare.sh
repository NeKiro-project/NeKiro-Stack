#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo 'usage: prepare.sh <absolute-components.json> <absolute-empty-work-root> <absolute-new-env-file>' >&2
  exit 2
fi

manifest_path=$1
work_root=$2
env_file=$3

for path in "$manifest_path" "$work_root" "$env_file"; do
  if [[ "$path" != /* ]]; then
    echo "path must be absolute: $path" >&2
    exit 2
  fi
done
if [[ ! -f "$manifest_path" ]]; then
  echo "component manifest does not exist: $manifest_path" >&2
  exit 1
fi
if [[ -e "$env_file" ]]; then
  echo "environment output already exists: $env_file" >&2
  exit 1
fi
if [[ -e "$work_root" ]]; then
  if [[ ! -d "$work_root" ]] || [[ -n "$(find "$work_root" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    echo "work root must be an empty directory: $work_root" >&2
    exit 1
  fi
else
  mkdir -p "$work_root"
fi

repository_url() {
  printf 'https://github.com/%s.git' "$1"
}

declare -A component_dirs
declare -A component_shas

while IFS=$'\t' read -r name repository commit_sha tag; do
  destination="$work_root/$name"
  url=$(repository_url "$repository")
  mkdir "$destination"
  git -C "$destination" init --quiet
  git -C "$destination" remote add origin "$url"
  git -C "$destination" fetch --quiet --depth=1 origin "$commit_sha"
  git -C "$destination" checkout --quiet --detach FETCH_HEAD
  actual_commit=$(git -C "$destination" rev-parse HEAD)
  if [[ "$actual_commit" != "$commit_sha" ]]; then
    echo "$name resolved to $actual_commit instead of $commit_sha" >&2
    exit 1
  fi
  git -C "$destination" fsck --full
  if [[ -n "$tag" ]]; then
    tag_rows=$(git ls-remote --tags "$url" "refs/tags/$tag" "refs/tags/$tag^{}")
    tag_commit=$(awk '$2 ~ /\^\{\}$/ { print $1 }' <<<"$tag_rows")
    if [[ -z "$tag_commit" ]]; then
      tag_commit=$(awk -v ref="refs/tags/$tag" '$2 == ref { print $1 }' <<<"$tag_rows")
    fi
    if [[ "$tag_commit" != "$commit_sha" ]]; then
      echo "$repository tag $tag resolves to ${tag_commit:-missing}, expected $commit_sha" >&2
      exit 1
    fi
  fi
  component_dirs[$name]=$destination
  component_shas[$name]=$commit_sha
done < <(go run ./cmd/manifest-validator -format tsv "$manifest_path")

control_plane_image="nekiro-control-plane:${component_shas[core]}"
router_image="nekiro-a2a-router:${component_shas[core]}"
runtime_a_image="nekiro-runtime-a:${component_shas[samples]}"
runtime_b_image="nekiro-runtime-b:${component_shas[samples]}"
secure_fixture_image="nekiro-nacos-secure-fixture:$(git rev-parse HEAD)"

docker build --file "${component_dirs[core]}/apps/control-plane/Dockerfile" --tag "$control_plane_image" "${component_dirs[core]}"
docker build --file "${component_dirs[core]}/apps/a2a-router/Dockerfile" --tag "$router_image" "${component_dirs[core]}"
docker build --file "${component_dirs[samples]}/runtime-a/Dockerfile" --tag "$runtime_a_image" "${component_dirs[samples]}"
docker build --file "${component_dirs[samples]}/runtime-b/Dockerfile" --tag "$runtime_b_image" "${component_dirs[samples]}"
docker build --file tests/fixtures/nacos-secure-fixture.Dockerfile --tag "$secure_fixture_image" .

{
  printf 'NEKIRO_CONTROL_PLANE_IMAGE=%q\n' "$control_plane_image"
  printf 'NEKIRO_A2A_ROUTER_IMAGE=%q\n' "$router_image"
  printf 'NEKIRO_RUNTIME_A_IMAGE=%q\n' "$runtime_a_image"
  printf 'NEKIRO_RUNTIME_B_IMAGE=%q\n' "$runtime_b_image"
  printf 'NEKIRO_NACOS_SECURE_PROXY_IMAGE=%q\n' "$secure_fixture_image"
  printf 'NEKIRO_STACK_CORE_DIR=%q\n' "${component_dirs[core]}"
  printf 'NEKIRO_STACK_CONSOLE_DIR=%q\n' "${component_dirs[console]}"
  printf 'NEKIRO_STACK_SDK_GO_DIR=%q\n' "${component_dirs[sdkGo]}"
  printf 'NEKIRO_STACK_SAMPLES_DIR=%q\n' "${component_dirs[samples]}"
  printf 'NEKIRO_STACK_TRANSPORT_GO_DIR=%q\n' "${component_dirs[transportGo]}"
} > "$env_file"

echo "prepared exact component images and wrote $env_file"
