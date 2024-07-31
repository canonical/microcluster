#!/bin/bash

set -e

cluster_flags=()

if [ -n "${DEBUG:-}" ]; then
  set -x
  cluster_flags+=("--debug")
  cluster_flags+=("--verbose")
fi

if [ -n "${CLUSTER_VERBOSE:-}" ]; then
  set -x
  cluster_flags+=("--verbose")
fi

test_dir="$(realpath -e "$(dirname -- "${BASH_SOURCE[0]}")")/system"

trap shutdown_systems EXIT HUP INT TERM

new_systems() {
  if [ -d "${test_dir}" ]; then
    rm -r "${test_dir}"
  fi

  microd_args=("${@}")

  for member in $(seq --format c%g "${1}"); do
    state_dir="${test_dir}/${member}"
    mkdir -p "${state_dir}"
    microd --state-dir "${state_dir}" "${cluster_flags[@]}" "${microd_args[@]:2}" &
    microctl --state-dir "${state_dir}" waitready
  done
}

bootstrap_systems() {
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

  indx=2
  for state_dir in "${test_dir}"/c?; do
    member=$(basename "${state_dir}")
    if [ "${member}" = "c1" ]; then
      continue
    fi

    token=$(microctl --state-dir "${test_dir}/c1" tokens add "${member}")

    microctl --state-dir "${state_dir}" init "${member}" "127.0.0.1:900${indx}" --token "${token}"

    indx=$((indx + 1))
  done

  # dqlite takes a while to form the cluster and assign roles to each node, and
  # microcluster takes a while to update the core_cluster_members table
  while [[ -n "$(microctl --state-dir "${state_dir}" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]]; do
    sleep 2
  done

  microctl --state-dir "${test_dir}/c1" cluster list
}

shutdown_systems() {
  if [ -n "${CLUSTER_INSPECT:-}" ]; then
    echo "Pausing to inspect... press enter when done"
    read -r
  fi

  for member in "${test_dir}"/c?; do
    microctl --state-dir "${member}" shutdown || true
  done

  sleep 2

  # The cluster doesn't always shut down right away; we've given it a chance
  for job_pid in $(jobs -p); do
    kill -9 "${job_pid}"
  done
}

test_misc() {
  new_systems 2 --heartbeat 2s

    # Ensure two daemons cannot start in the same state dir
  ! microd --state-dir "${test_dir}/c1" "${cluster_flags[@]}" || false

  # Ensure only valid member names are used for bootstrap
  ! microctl --state-dir "${test_dir}/c1" init "c/1" 127.0.0.1:9001 --bootstrap || false

  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

  # Ensure only valid member names are used for join
  token_node2=$(microctl --state-dir "${test_dir}/c1" tokens add "c/2")
  ! microctl --state-dir "${test_dir}/c2" init "c/2" 127.0.0.1:9002 --token "${token_node2}" || false

  shutdown_systems
}

test_tokens() {
  new_systems 3 --heartbeat 4s
  bootstrap_systems

  microctl --state-dir "${test_dir}/c1" tokens add default_expiry

  microctl --state-dir "${test_dir}/c1" tokens add short_expiry --expire-after 1s

  microctl --state-dir "${test_dir}/c1" tokens add long_expiry --expire-after 400h

  sleep 1

  ! microctl --state-dir "${test_dir}/c1" tokens list --format csv | grep -q short_expiry || false
  microctl --state-dir "${test_dir}/c1" tokens list --format csv | grep -q default_expiry
  microctl --state-dir "${test_dir}/c1" tokens list --format csv | grep -q long_expiry

  # Ensure expired tokens cannot be used to join the cluster
  mkdir -p "${test_dir}/c4"
  microd --state-dir "${test_dir}/c4" "${cluster_flags[@]}" &
  microctl --state-dir "${test_dir}/c4" waitready

  token=$(microctl --state-dir "${test_dir}/c1" tokens add "c4" --expire-after 1s)

  sleep 1

  ! microctl --state-dir "${test_dir}/c4" init "c4" "127.0.0.1:9005" --token "${token}" || false

  shutdown_systems
}

test_recover() {
  new_systems 5 --heartbeat 2s
  bootstrap_systems
  shutdown_systems

  microctl --state-dir "${test_dir}/c1" cluster list --local --format yaml |
    yq '
      sort_by(.name) |
      .[0].role = "voter" |
      .[1].role = "voter" |
      .[2].role = "spare" |
      .[3].role = "spare" |
      .[4].role = "spare"' |
    sed 's/:900/:800/' |
    microctl --state-dir "${test_dir}/c1" cluster edit

  # While it is perfectly fine to load the recovery tarball on the member where it
  # was generated, the tests should make sure that both codepaths work, i.e. we
  # should make sure that recovery leaves the database ready to start with the
  # new configuration without needing to load the recovery tarball.
  mv "${test_dir}/c1/recovery_db.tar.gz" "${test_dir}/c2/"

  for member in c1 c2; do
    state_dir="${test_dir}/${member}"
    microd --state-dir "${state_dir}" "${cluster_flags[@]}" --heartbeat 2s &
  done
  microctl --state-dir "${test_dir}/c1" waitready

  # Allow for a round of heartbeats to update the member roles in core_cluster_members
  sleep 3

  microctl --state-dir "${test_dir}/c1" cluster list

  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c1").role') == "voter" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c2").role') == "voter" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c3").role') == "spare" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c4").role') == "spare" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c5").role') == "spare" ]]

  shutdown_systems
}

# allow for running a specific set of tests
if [ "${1:-"all"}" = "all" ] || [ "${1}" = "" ]; then
  test_misc
  test_tokens
  test_recover
elif [ "${1}" = "recover" ]; then
  test_recover
elif [ "${1}" = "tokens" ]; then
  test_tokens
elif [ "${1}" = "misc" ]; then
  test_misc
else
  echo "Unknown test ${1}"
fi
