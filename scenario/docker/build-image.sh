#!/bin/bash
set -euo pipefail

architecture="$1"
image_name="$2"
repository="$3"
branch="$4"
artifact_directory="${AZL_IMAGE_BUILD_ARTIFACTS:-/artifacts}"
checkout="$(mktemp -d)"
output_directory="$checkout/base/out/images/$image_name"
artifact="$artifact_directory/$image_name.qcow2"

if [[ -e "$artifact" || -L "$artifact" ]]; then
    echo "$image_name.qcow2 already exists" >&2
    exit 1
fi

git clone --depth=1 --branch "$branch" "$repository" "$checkout"
git -C "$checkout" rev-parse HEAD | tee "$artifact_directory/source-revision"

cd "$checkout"
azldev image build "$image_name" --arch "$architecture"

shopt -s nullglob
images=("$output_directory"/*.raw)
if [[ "${#images[@]}" -ne 1 || ! -f "${images[0]}" ]]; then
    echo "expected exactly one $image_name raw image" >&2
    exit 1
fi

temporary="$(mktemp "$artifact_directory/.$image_name.qcow2.XXXXXX")"
trap 'rm -f "$temporary"' EXIT

qemu-img convert -f raw -O qcow2 -c "${images[0]}" "$temporary"
qemu-img check "$temporary"
ln -T -- "$temporary" "$artifact"

rm -f "$temporary"
trap - EXIT
