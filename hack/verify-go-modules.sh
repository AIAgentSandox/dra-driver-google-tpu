#!/usr/bin/env bash

# Copyright The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Verifies that go.mod, go.sum and vendor/ are tidy and in sync.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail() {
	echo "ERROR: $*" >&2
	echo "Run 'go mod tidy && make vendor' and commit go.mod, go.sum and vendor/." >&2
	exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Checking that go.mod and go.sum are tidy"
if ! go mod tidy -diff >"$tmp/tidy.diff"; then
	cat "$tmp/tidy.diff"
	fail "go.mod or go.sum is not tidy."
fi

echo "Checking that vendor/ matches go.mod"
go mod vendor -o "$tmp/vendor"
if ! diff -r vendor "$tmp/vendor"; then
	fail "vendor/ is out of date."
fi

echo "Checking that no vendored file is git-ignored"
ignored=$(find vendor -type f | git check-ignore --no-index --stdin || true)
if [ -n "$ignored" ]; then
	echo "$ignored"
	fail "the files above are vendored but ignored by .gitignore, so they are never committed."
fi

echo "Verifying module checksums"
go mod verify
# Load every package the module needs from go.mod and go.sum alone, ignoring
# vendor/. This is 'go list -m all' in cri-tools, but that cannot work here:
# k8s.io/kubernetes requires its staging modules (k8s.io/kubectl, ...) at
# v0.0.0, which only its own replace directives resolve.
go list -mod=readonly all >/dev/null

echo "Go modules are tidy."
