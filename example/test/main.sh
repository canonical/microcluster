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
  token_node2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  ! microctl --state-dir "${test_dir}/c2" init "c/2" 127.0.0.1:9002 --token "${token_node2}" || false

  shutdown_systems
}

test_tokens() {
  new_systems 3 --heartbeat 4s
  bootstrap_systems

  # Ensure tokens with invalid names cannot be created
  ! microctl --state-dir "${test_dir}/c1" tokens add ""
  ! microctl --state-dir "${test_dir}/c1" tokens add "invalid_name"
  ! microctl --state-dir "${test_dir}/c1" tokens add "invalid_"
  ! microctl --state-dir "${test_dir}/c1" tokens add "_invalid"
  ! microctl --state-dir "${test_dir}/c1" tokens add "invalid."
  ! microctl --state-dir "${test_dir}/c1" tokens add ".invalid"

  microctl --state-dir "${test_dir}/c1" tokens add default-expiry

  microctl --state-dir "${test_dir}/c1" tokens add short-expiry --expire-after 1s

  microctl --state-dir "${test_dir}/c1" tokens add long-expiry --expire-after 400h

  sleep 1

  ! microctl --state-dir "${test_dir}/c1" tokens list --format csv | grep -q short-expiry || false
  microctl --state-dir "${test_dir}/c1" tokens list --format csv | grep -q default-expiry
  microctl --state-dir "${test_dir}/c1" tokens list --format csv | grep -q long-expiry

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

test_join_token_after_cluster_formed() {
  # Test node join succeeds when original control plane is down
  # Token generated AFTER the 3-node cluster is formed
  # Based on k8s-snap test: test_node_join_succeeds_when_original_control_plane_is_down
  
  echo "Starting join test - token generated after 3-node cluster formed"
  
  new_systems 4 --heartbeat 2s
  
  # Bootstrap initial cluster with c1 as original control plane
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap
  
  # Get join tokens for c2 and c3 while c1 is available
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  
  # Join c2 and c3 to form 3-node cluster
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}"
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}"
  
  # Wait for cluster to stabilize
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]]; do
    sleep 2
  done
  
  echo "Initial 3-node cluster formed:"
  microctl --state-dir "${test_dir}/c1" cluster list
  
  # Verify all nodes are voters in the 3-node cluster
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c1").role') == "voter" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c2").role') == "voter" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c3").role') == "voter" ]]
  
  # Get join token for c4 while c1 is still available
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")
  
  echo "Simulating original control plane (c1) failure..."
  
  # Kill c1 to simulate original control plane failure
  c1_pid=$(jobs -p | head -1)  # Get first background job (should be c1)
  kill -9 "${c1_pid}" 2>/dev/null || true
  
  sleep 2
  
  echo "Attempting to join c4 while c1 is down (this tests fault tolerance)..."
  
  # This is the critical test: can c4 join while c1 is down?
  # This should work with proper fault tolerance implementation
  microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}"
  
  echo "Verifying cluster state from surviving nodes..."
  
  # Verify cluster from c2's perspective (c1 should be unreachable, c2/c3/c4 should be online)
  microctl --state-dir "${test_dir}/c2" cluster list
  
  # Wait for roles to stabilize after c1 failure and c4 join
  sleep 5
  
  # Count online nodes from c2's perspective
  online_count=$(microctl --state-dir "${test_dir}/c2" cluster list -f yaml | yq '[.[] | select(.status == "ONLINE")] | length')
  echo "Online nodes count: ${online_count}"
  
  # Should have 3 online nodes (c2, c3, c4) even though c1 is down
  [[ "${online_count}" -eq 3 ]] || {
    echo "ERROR: Expected 3 online nodes, got ${online_count}"
    microctl --state-dir "${test_dir}/c2" cluster list
    return 1
  }
  
  # Verify c4 successfully joined
  c4_status=$(microctl --state-dir "${test_dir}/c2" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c4").status')
  [[ "${c4_status}" == "ONLINE" ]] || {
    echo "ERROR: c4 should be ONLINE, got ${c4_status}"
    return 1
  }
  
  # Verify c4 is a voter, not just PENDING
  # Add retry logic to wait for role promotion
  echo "Waiting for c4 to be promoted from PENDING to voter..."
  retry_count=0
  max_retries=10
  while [[ "${retry_count}" -lt "${max_retries}" ]]; do
    c4_role=$(microctl --state-dir "${test_dir}/c2" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c4").role')
    if [[ "${c4_role}" == "voter" ]]; then
      break
    fi
    echo "c4 role is still ${c4_role}, waiting for promotion... (attempt $((retry_count + 1))/${max_retries})"
    sleep 2
    retry_count=$((retry_count + 1))
  done
  
  [[ "${c4_role}" == "voter" ]] || {
    echo "ERROR: c4 should be voter, got ${c4_role} after ${max_retries} attempts"
    microctl --state-dir "${test_dir}/c2" cluster list
    return 1
  }

  # Verify dqlite cluster.yaml shows 4 members (low-level dqlite validation)
  dqlite_cluster_count=$(yq '. | length' "${test_dir}/c2/database/cluster.yaml")
  [[ "${dqlite_cluster_count}" -eq 4 ]] || {
    echo "ERROR: Expected exactly 4 members in dqlite cluster.yaml, got ${dqlite_cluster_count}"
    echo "Dqlite cluster.yaml contents:"
    cat "${test_dir}/c2/database/cluster.yaml"
    return 1
  }

  echo "SUCCESS: Node c4 successfully joined cluster while original control plane c1 was down"
  echo "SUCCESS: Node c4 is ONLINE and has voter role"
  echo "Final cluster state:"
  microctl --state-dir "${test_dir}/c2" cluster list
  echo "Dqlite cluster members:"
  cat "${test_dir}/c2/database/cluster.yaml"
  
  shutdown_systems
}

test_join_token_before_cluster_formed() {
  # Test node join succeeds when original control plane is down
  # Token generated BEFORE other nodes join (while c1 is solo)
  # Based on k8s-snap test: test_node_join_succeeds_when_original_control_plane_is_down
  
  echo "Starting join test - token generated before cluster formed"
  
  new_systems 4 --heartbeat 2s
  
  # Bootstrap initial cluster with c1 as original control plane
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap
  
  # Get join tokens for c2 and c3 while c1 is available
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  # Get join token for c4 while c1 is the only node up
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")
  
  # Join c2 and c3 to form 3-node cluster
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}"
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}"
  
  # Wait for cluster to stabilize
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]]; do
    sleep 2
  done
  
  echo "Initial 3-node cluster formed:"
  microctl --state-dir "${test_dir}/c1" cluster list
  
  # Verify all nodes are voters in the 3-node cluster
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c1").role') == "voter" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c2").role') == "voter" ]]
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c3").role') == "voter" ]]
  
  echo "Simulating original control plane (c1) failure..."
  
  # Kill c1 to simulate original control plane failure
  c1_pid=$(jobs -p | head -1)  # Get first background job (should be c1)
  kill -9 "${c1_pid}" 2>/dev/null || true
  
  sleep 2
  
  echo "Attempting to join c4 while c1 is down (this should fail cleanly)..."
  
  # This is the critical test: c4 should FAIL to join when c1 is down
  # and the token was generated before cluster formation
  # The join should fail cleanly without creating partial state
  ! microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}" || {
    echo "ERROR: c4 join should have failed but succeeded"
    return 1
  }
  
  echo "c4 join failed as expected. Verifying clean failure..."
  
  # Verify cluster from c2's perspective (should only show c1 as unreachable, c2/c3 as online)
  microctl --state-dir "${test_dir}/c2" cluster list
  
  # Wait a bit to see if c4 appears in cluster or fails cleanly
  sleep 5
  
  # Count online nodes from c2's perspective - should only be c2 and c3
  online_count=$(microctl --state-dir "${test_dir}/c2" cluster list -f yaml | yq '[.[] | select(.status == "ONLINE")] | length')
  echo "Online nodes count: ${online_count}"
  
  # Should have only 2 online nodes (c2, c3) since c1 is down and c4 should fail to join
  [[ "${online_count}" -eq 2 ]] || {
    echo "ERROR: Expected 2 online nodes (c2, c3), got ${online_count}"
    microctl --state-dir "${test_dir}/c2" cluster list
    return 1
  }
  
  # Verify c4 is NOT in the cluster members table (clean failure)
  c4_exists=$(microctl --state-dir "${test_dir}/c2" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c4")' | wc -l)
  [[ "${c4_exists}" -eq 0 ]] || {
    echo "ERROR: c4 should not appear in cluster members table (partial join detected)"
    echo "c4 state in cluster:"
    microctl --state-dir "${test_dir}/c2" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c4")'
    return 1
  }
  
  # Verify that only c1, c2, c3 exist in cluster (no c4)
  member_count=$(microctl --state-dir "${test_dir}/c2" cluster list -f yaml | yq '. | length')
  [[ "${member_count}" -eq 3 ]] || {
    echo "ERROR: Expected exactly 3 cluster members (c1, c2, c3), got ${member_count}"
    microctl --state-dir "${test_dir}/c2" cluster list
    return 1
  }
  
  # Verify dqlite cluster.yaml shows only 3 members (low-level dqlite validation)
  dqlite_cluster_count=$(yq '. | length' "${test_dir}/c2/database/cluster.yaml")
  [[ "${dqlite_cluster_count}" -eq 3 ]] || {
    echo "ERROR: Expected exactly 3 members in dqlite cluster.yaml, got ${dqlite_cluster_count}"
    echo "Dqlite cluster.yaml contents:"
    cat "${test_dir}/c2/database/cluster.yaml"
    return 1
  }
  
  # Verify c4 (127.0.0.1:9004) is NOT in dqlite cluster.yaml
  c4_in_dqlite_yaml=$(yq '.[] | select(.Address == "127.0.0.1:9004")' "${test_dir}/c2/database/cluster.yaml" | wc -l)
  [[ "${c4_in_dqlite_yaml}" -eq 0 ]] || {
    echo "ERROR: c4 found in dqlite cluster.yaml (partial join detected at dqlite level)"
    echo "Dqlite cluster.yaml contents:"
    cat "${test_dir}/c2/database/cluster.yaml"
    return 1
  }
  
  echo "SUCCESS: Node c4 failed to join cleanly - no partial join state detected"
  echo "SUCCESS: Verified at both microcluster API and go-dqlite cluster members"
  echo "Final cluster state (c4 should not appear):"
  microctl --state-dir "${test_dir}/c2" cluster list
  echo "Dqlite cluster members:"
  cat "${test_dir}/c2/database/cluster.yaml"
  
  shutdown_systems
}

# allow for running a specific set of tests
if [ "${1:-"all"}" = "all" ] || [ "${1}" = "" ]; then
  test_misc
  test_tokens
  test_recover
  test_join_token_after_cluster_formed
  test_join_token_before_cluster_formed
elif [ "${1}" = "recover" ]; then
  test_recover
elif [ "${1}" = "tokens" ]; then
  test_tokens
elif [ "${1}" = "misc" ]; then
  test_misc
elif [ "${1}" = "join-after" ]; then
  test_join_token_after_cluster_formed
elif [ "${1}" = "join-before" ]; then
  test_join_token_before_cluster_formed
else
  echo "Unknown test ${1}"
fi
