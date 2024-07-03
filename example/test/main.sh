#!/bin/bash

set -e

cluster_flags=()

if [ -n "${DEBUG:-}" ]; then
  set -x
  cluster_flags+=("--debug")
  cluster_flags+=("--verbose")
fi

if [ -n "${CLUSTER_VERBOSE:-}" ]; then
  cluster_flags+=("--verbose")
fi

test_dir="$(realpath -e "$(dirname -- "${BASH_SOURCE[0]}")")/system"

if [ -d "${test_dir}" ]; then
  rm -r "${test_dir}"
fi

members=("c1" "c2" "c3" "c4" "c5")

for member in "${members[@]}"; do
  state_dir="${test_dir}/${member}"
  mkdir -p "${state_dir}"
  microd --state-dir "${state_dir}" "${cluster_flags[@]}" &
  microctl --state-dir "${state_dir}" waitready
done

# Ensure two daemons cannot start in the same state dir
! microd --state-dir "${test_dir}/c1" "${cluster_flags[@]}"

# Ensure only valid member names are used for bootstrap
! microctl --state-dir "${test_dir}/c1" init "c/1" 127.0.0.1:9001 --bootstrap

microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

# Ensure only valid member names are used for join
token_node2=$(microctl --state-dir "${test_dir}/c1" tokens add "c/2")
! microctl --state-dir "${test_dir}/c1" init "c/2" 127.0.0.1:9002 --token "${token_node2}"

indx=2
for member in "${members[@]:1}"; do
  token=$(microctl --state-dir "${test_dir}/c1" tokens add "${member}")

  microctl --state-dir "${test_dir}/${member}" init "${member}" "127.0.0.1:900${indx}" --token "${token}"

  indx=$((indx + 1))
done

# dqlite takes a while to form the cluster and assign roles to each node, and
# microcluster takes a while to update the core_cluster_members table
while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]]; do
  sleep 2
done

microctl --state-dir "${test_dir}/c1" cluster list

# Clean up
if [ -n "${CLUSTER_INSPECT:-}" ]; then
  echo "Pausing to inspect... press enter when done"
  read -r
fi

for member in "${members[@]}"; do
  microctl --state-dir "${test_dir}/${member}" shutdown
done

# The cluster doesn't always shut down right away; this is fine since we're
# doing recovery next
for jobnum in {1..5}; do
  kill -9 %"${jobnum}"
done

microctl --state-dir "${test_dir}/c1" cluster list --local --format yaml |
  yq '
    sort_by(.name) |
    .[0].role = "voter" |
    .[1].role = "voter" |
    .[2].role = "spare" |
    .[3].role = "spare" |
    .[4].role = "spare"' |
  microctl --state-dir "${test_dir}/c1" cluster edit

cp "${test_dir}/c1/recovery_db.tar.gz" "${test_dir}/c2/"

for member in c1 c2; do
  state_dir="${test_dir}/${member}"
  microd --state-dir "${state_dir}" "${cluster_flags[@]}" > /dev/null 2>&1 &
done

# microcluster takes a long time to update the member roles in the core_cluster_members table
sleep 90

microctl --state-dir "${test_dir}/c1" cluster list

[[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c1").role') == "voter" ]]
[[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c2").role') == "voter" ]]
[[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c3").role') == "spare" ]]
[[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c4").role') == "spare" ]]
[[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c5").role') == "spare" ]]

echo "Tests passed"

kill 0
