#!/bin/bash

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

members=("c1" "c2" "c3")

for member in "${members[@]}"; do
  state_dir="${test_dir}/${member}"
  mkdir -p "${state_dir}"
  microd --state-dir "${state_dir}" "${cluster_flags[@]}" &
  microctl --state-dir "${state_dir}" waitready
done

# Ensure two daemons cannot start in the same state dir
! microd --state-dir "${test_dir}/c1" "${cluster_flags[@]}"

# Ensure only valid member names are used
! microctl --state-dir "${test_dir}/c1" init "c/1" 127.0.0.1:9001 --bootstrap

microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

# Ensure only valid member names are used
token_node2=$(microctl --state-dir "${test_dir}/c1" tokens add "c/2")
! microctl --state-dir "${test_dir}/c1" init "c/2" 127.0.0.1:9003 --token "${token_node2}"

token_node2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
token_node3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")

microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_node2}"
microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_node3}"

# Clean up
if [ -n "${CLUSTER_INSPECT:-}" ]; then
  echo "Pausing to inspect... press enter when done"
  read -r
fi

kill %1
kill %2
kill %3
