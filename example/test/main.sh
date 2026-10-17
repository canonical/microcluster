#!/bin/bash

set -e

cluster_flags=()

if [ -n "${DEBUG:-}" ]; then
  set -x
  cluster_flags+=("--debug")
fi

test_dir="$(realpath -e "$(dirname -- "${BASH_SOURCE[0]}")")/system"

# Must be set before cleanup().
TEST_CURRENT="setup"
TEST_RESULT="failure"

declare -A test_results

cleanup() {
  shutdown_systems

  echo ""
  echo ""
  echo "==> Test result: ${TEST_RESULT}"
}

trap cleanup EXIT HUP INT TERM

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
    # Check if process still exists before trying to kill it
    if kill -0 "${job_pid}" 2>/dev/null; then
      kill -9 "${job_pid}" 2>/dev/null || true
    fi
  done
}

run_test() {
  local test_name="${1}"

  TEST_CURRENT="${test_name}"
  echo "==> TEST BEGIN: ${TEST_CURRENT}"

  if "test_${test_name}"; then
    test_results["${test_name}"]="PASS"
  else
    test_results["${test_name}"]="FAIL"
  fi
  echo "==> TEST DONE: ${TEST_CURRENT}"

  if [ "${test_results[${test_name}]}" != "PASS" ]; then
    TEST_RESULT="failure"
    return 1
  fi
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

test_membership_consistency() {
  echo "Testing membership consistency checks"

  new_systems 4 --heartbeat 2s

  # Bootstrap first member (daemon already running from new_systems)
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

  # Join second member (daemon already running)
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}"

  # Start third member and join cluster
  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}"

  # Fetch join token for c4
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")

  # Wait for cluster to stabilize
  echo "  -> Waiting for cluster members to exit PENDING state"

  # Wait for all members to be promoted from PENDING
  retry_count=0
  max_retries=10
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]] && [[ ${retry_count} -lt ${max_retries} ]]; do
    echo "  -> Still waiting for members to exit PENDING state..."
    sleep 2
    retry_count=$((retry_count + 1))
  done

  echo "  -> Cluster established successfully"

  # Verify cluster is healthy
  cluster_size=$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '. | length')
  if [ "${cluster_size}" != "3" ]; then
    echo "ERROR: Expected cluster size 3, got ${cluster_size}"
    exit 1
  fi

  # Simulate inconsistent state by directly manipulating the database
  # while keeping dqlite/truststore intact
  echo "  -> Simulating inconsistent membership state"

  # Remove c2's membership from core_cluster_members (simulating partial remove failure)
  microctl --state-dir "${test_dir}/c1" sql "DELETE FROM core_cluster_members WHERE name = 'c2'"

  echo "  -> Created inconsistent state (c2 removed from database but still in truststore until heartbeat timeout)"

  # Test member removal with inconsistent state
  echo "  -> Testing member removal with inconsistent state"
  if microctl --state-dir "${test_dir}/c1" cluster remove c2 2>/tmp/remove_error; then
    echo "ERROR: Member removal should have failed due to inconsistent state"
    cat /tmp/remove_error
    exit 1
  else
    echo "  -> Member removal correctly blocked due to membership inconsistency"
    cat /tmp/remove_error
  fi

  # Try to join a new member - this should fail due to inconsistency
  echo "  -> Testing join of new member c4 with inconsistent state"
  if microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}" 2>/tmp/join_error; then
    echo "ERROR: Member c4 should not have been able to join due to inconsistent state"
    cat /tmp/join_error
    exit 1
  else
    echo "  -> Membership inconsistency correctly detected, c4 join blocked"
    cat /tmp/join_error
  fi

  # Attempt to generate token should fail
  echo "  -> Testing token generation with inconsistent state"
  if microctl --state-dir "${test_dir}/c1" tokens add c5 2>/tmp/token_error; then
    echo "ERROR: Token generation should have failed due to inconsistent state"
    cat /tmp/token_error
    exit 1
  else
    echo "  -> Token generation correctly blocked due to membership inconsistency"
    cat /tmp/token_error
  fi

  # Test member force flag overrides during removal with inconsistent state works
  echo "  -> Testing member force removal with inconsistent state"
  if microctl --state-dir "${test_dir}/c1" cluster remove c2 --force --address 127.0.0.1:9002; then
    echo "  -> Member c2 force removal succeeded as expected"
  else
    echo "ERROR: Member force removal should have succeeded despite inconsistent state"
    exit 1
  fi

  # Generating a new token should now succeed
  echo "  -> Testing token generation after resolving inconsistency"
  if microctl --state-dir "${test_dir}/c1" tokens add c5 2>/tmp/token_resp; then
    echo "  -> Token generation succeeded as expected after resolving inconsistency"
    cat /tmp/token_resp
  else
    echo "ERROR: Token generation should have succeeded after resolving inconsistency"
    cat /tmp/token_resp
    exit 1
  fi
  echo "  -> Membership consistency checks working as expected"

  shutdown_systems
}

test_truststore_force_removal() {
  echo "Testing force removal"

  new_systems 3 --heartbeat 2s

  # Bootstrap first member
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

  # Join second and third members
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}"

  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}"

  # Wait for cluster to stabilize
  echo "  -> Waiting for cluster to stabilize"
  retry_count=0
  max_retries=10
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]] && [[ ${retry_count} -lt ${max_retries} ]]; do
    sleep 2
    retry_count=$((retry_count + 1))
  done

  echo "  -> Cluster established with 3 members"
  microctl --state-dir "${test_dir}/c1" cluster list

  # Test force removal of non-existing member and random address
  echo "  -> Testing force removal of non-existing member with random address"
  if microctl --state-dir "${test_dir}/c1" cluster remove nonexist --force --address 127.0.0.1:9999 2>/tmp/remove_error; then
    echo "ERROR: Force removal of non-existing member should have failed"
    cat /tmp/remove_error
    exit 1
  else
    echo "  -> Force removal of non-existing member failed as expected"
    cat /tmp/remove_error
  fi

  # Simulate truststore corruption: remove c3 from truststore while keeping DB and dqlite entries
  # Need to remove from all nodes' truststores to prevent repopulation
  echo "  -> Simulating truststore deletion of c3 from all nodes (keeping DB and dqlite entries)"
  rm -f "${test_dir}/c1/truststore/c3.yaml"
  rm -f "${test_dir}/c2/truststore/c3.yaml"
  rm -f "${test_dir}/c3/truststore/c3.yaml"

  # Attempt normal removal should fail (membership inconsistency detected)
  echo "  -> Testing normal removal of c3 (should fail due to missing truststore)"
  if microctl --state-dir "${test_dir}/c1" cluster remove c3 --address 127.0.0.1:9003 2>/tmp/remove_error; then
    echo "ERROR: Normal removal should have failed"
    exit 1
  else
    echo "  -> Normal removal blocked as expected"
    cat /tmp/remove_error
  fi

  # Force remove with explicit address should succeed
  echo "  -> Testing force removal of c3 with address override"
  if microctl --state-dir "${test_dir}/c1" cluster remove c3 --force --address 127.0.0.1:9003; then
    echo "  -> Force removal of c3 succeeded"
  else
    echo "ERROR: Force removal should have succeeded"
    exit 1
  fi

  # Now generate a new token - this should succeed because membership is now consistent
  echo "  -> Testing token generation after force removal (should succeed)"
  if microctl --state-dir "${test_dir}/c1" tokens add "c4" 2>/tmp/token_resp; then
    echo "  -> Token generation succeeded - membership is now consistent"
    cat /tmp/token_resp
  else
    echo "ERROR: Token generation should have succeeded after force removal"
    cat /tmp/token_resp
    exit 1
  fi

  echo "SUCCESS: Force removal of non-existing member and random address blocked as expected"
  echo "SUCCESS: Force removal of truststore-orphaned node successful"
  echo "SUCCESS: Verified membership consistency restored after force removal"

  shutdown_systems
}

test_parallel_joins() {
  echo "Testing parallel joins"

  new_systems 4 --heartbeat 2s

  # Bootstrap first member
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

  # Prepare tokens for remaining members
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")

  # Kick off joins in parallel and collect PIDs
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}" &
  pids=($!)
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}" &
  pids+=($!)
  microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}" &
  pids+=($!)

  for pid in "${pids[@]}"; do
    if ! wait "${pid}"; then
      echo "ERROR: parallel join failed (pid ${pid})"
      return 1
    fi
  done

  # Wait for cluster to stabilize
  retry_count=0
  max_retries=10
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]] && [[ ${retry_count} -lt ${max_retries} ]]; do
    sleep 2
    retry_count=$((retry_count + 1))
  done

  cluster_size=$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '. | length')
  if [ "${cluster_size}" != "4" ]; then
    echo "ERROR: Expected cluster size 4 after parallel joins, got ${cluster_size}"
    return 1
  fi

  echo "SUCCESS: parallel joins completed without failures"

  shutdown_systems
}

test_daemon_config_api() {
  echo "Testing daemon/config API"

  new_systems 2 --heartbeat 2s
  bootstrap_systems

  socket_path="${test_dir}/c1/control.socket"

  daemon_config_get() {
    curl -sS --unix-socket "${socket_path}" \
      http://unix/core/1.0/daemon/config
  }

  daemon_config_put() {
    local payload="${1}"
    local query_suffix=""
    if [ -n "${2:-}" ]; then
      query_suffix="?restart=${2}"
    fi

    curl -sS --unix-socket "${socket_path}" \
      -X PUT \
      -H "Content-Type: application/json" \
      -d "${payload}" \
      "http://unix/core/1.0/daemon/config${query_suffix}"
  }

  daemon_config_patch() {
    local payload="${1}"
    local query_suffix=""
    if [ -n "${2:-}" ]; then
      query_suffix="?restart=${2}"
    fi

    curl -sS --unix-socket "${socket_path}" \
      -X PATCH \
      -H "Content-Type: application/json" \
      -d "${payload}" \
      "http://unix/core/1.0/daemon/config${query_suffix}"
  }

  json_get() {
    local payload="${1}"
    local expr="${2}"

    echo "${payload}" | yq -r "${expr}"
  }

  response_code() {
    local payload="${1}"

    local status_code
    status_code="$(json_get "${payload}" '.status_code')"
    if [ "${status_code}" != "0" ]; then
      echo "${status_code}"
      return
    fi

    json_get "${payload}" '.error_code'
  }

  # Initial state: GET should return the bootstrap member identity and the default failure-domain.
  echo "==> initial state and PUT semantics"
  config_resp="$(daemon_config_get)"
  [[ "$(response_code "${config_resp}")" == "200" ]] || {
    echo "ERROR: Failed to GET daemon config"
    echo "${config_resp}"
    return 1
  }
  [[ "$(json_get "${config_resp}" '.metadata.name')" == "c1" ]] || {
    echo "ERROR: Expected daemon name c1"
    echo "${config_resp}"
    return 1
  }
  [[ "$(json_get "${config_resp}" '.metadata.address')" == "127.0.0.1:9001" ]] || {
    echo "ERROR: Expected daemon address 127.0.0.1:9001"
    echo "${config_resp}"
    return 1
  }
  [[ "$(json_get "${config_resp}" '.metadata."failure-domain"')" == "0" ]] || {
    echo "ERROR: Expected initial failure-domain to be 0"
    echo "${config_resp}"
    return 1
  }
  # Full replacement: PUT sets failure-domain and clears servers when servers are omitted.
  update_payload='{"name":"c1","address":"127.0.0.1:9001","failure-domain":9}'
  update_resp="$(daemon_config_put "${update_payload}")"
  [[ "$(response_code "${update_resp}")" == "200" ]] || {
    echo "ERROR: Failed to update daemon config"
    echo "${update_resp}"
    return 1
  }

  config_resp="$(daemon_config_get)"
  [[ "$(json_get "${config_resp}" '.metadata."failure-domain"')" == "9" ]] || {
    echo "ERROR: Expected failure-domain to be updated to 9"
    echo "${config_resp}"
    return 1
  }
  [[ "$(json_get "${config_resp}" '.metadata.servers | length')" == "0" ]] || {
    echo "ERROR: Expected servers map to be empty after PUT"
    echo "${config_resp}"
    return 1
  }
  grep -q "^failure-domain: 9$" "${test_dir}/c1/daemon.yaml" || {
    echo "ERROR: daemon.yaml missing failure-domain persistence"
    cat "${test_dir}/c1/daemon.yaml"
    return 1
  }
  # Full replacement: PUT without failure-domain resets it to the default value.
  update_payload='{"name":"c1","address":"127.0.0.1:9001"}'
  update_resp="$(daemon_config_put "${update_payload}")"
  [[ "$(response_code "${update_resp}")" == "200" ]] || {
    echo "ERROR: Failed to PUT daemon config without failure-domain"
    echo "${update_resp}"
    return 1
  }

  config_resp="$(daemon_config_get)"
  [[ "$(json_get "${config_resp}" '.metadata."failure-domain"')" == "0" ]] || {
    echo "ERROR: Expected failure-domain to be cleared by PUT (full replacement)"
    echo "${config_resp}"
    return 1
  }
  echo "==> PATCH semantics"

  # Prime a known failure-domain value so PATCH preservation semantics are observable.
  update_payload='{"name":"c1","address":"127.0.0.1:9001","failure-domain":5}'
  update_resp="$(daemon_config_put "${update_payload}")"
  [[ "$(response_code "${update_resp}")" == "200" ]] || {
    echo "ERROR: Failed to PUT failure-domain=5"
    echo "${update_resp}"
    return 1
  }

  # Partial update: omitting failure-domain preserves the existing value.
  patch_payload='{"name":"c1","address":"127.0.0.1:9001"}'
  patch_resp="$(daemon_config_patch "${patch_payload}")"
  [[ "$(response_code "${patch_resp}")" == "200" ]] || {
    echo "ERROR: PATCH daemon config failed"
    echo "${patch_resp}"
    return 1
  }

  config_resp="$(daemon_config_get)"
  [[ "$(json_get "${config_resp}" '.metadata."failure-domain"')" == "5" ]] || {
    echo "ERROR: PATCH should preserve failure-domain when omitted (expected 5)"
    echo "${config_resp}"
    return 1
  }
  # Partial update: PATCH can update failure-domain directly and should persist it to daemon.yaml.
  patch_payload='{"name":"c1","address":"127.0.0.1:9001","failure-domain":7}'
  patch_resp="$(daemon_config_patch "${patch_payload}")"
  [[ "$(response_code "${patch_resp}")" == "200" ]] || {
    echo "ERROR: PATCH with failure-domain failed"
    echo "${patch_resp}"
    return 1
  }

  config_resp="$(daemon_config_get)"
  [[ "$(json_get "${config_resp}" '.metadata."failure-domain"')" == "7" ]] || {
    echo "ERROR: Expected failure-domain to be updated to 7 via PATCH"
    echo "${config_resp}"
    return 1
  }
  grep -q "^failure-domain: 7$" "${test_dir}/c1/daemon.yaml" || {
    echo "ERROR: daemon.yaml missing PATCH failure-domain persistence"
    cat "${test_dir}/c1/daemon.yaml"
    return 1
  }
  # PATCH ignores name and address fields because they are not part of DaemonConfigPatch.
  # A payload containing different values should succeed without applying them.
  ignore_payload='{"name":"renamed","address":"127.0.0.1:9999","failure-domain":7}'
  ignore_resp="$(daemon_config_patch "${ignore_payload}")"
  [[ "$(response_code "${ignore_resp}")" == "200" ]] || {
    echo "ERROR: PATCH with unknown fields should succeed (name/address are ignored)"
    echo "${ignore_resp}"
    return 1
  }

  config_resp="$(daemon_config_get)"
  [[ "$(json_get "${config_resp}" '.metadata.name')" == "c1" ]] || {
    echo "ERROR: PATCH should not have changed the name"
    echo "${config_resp}"
    return 1
  }
  [[ "$(json_get "${config_resp}" '.metadata.address')" == "127.0.0.1:9001" ]] || {
    echo "ERROR: PATCH should not have changed the address"
    echo "${config_resp}"
    return 1
  }
  echo "==> validation failures"

  # PUT rejects a changed name because name is immutable.
  bad_payload='{"name":"renamed","address":"127.0.0.1:9001","servers":{},"failure-domain":9}'
  bad_resp="$(daemon_config_put "${bad_payload}")"
  [[ "$(response_code "${bad_resp}")" == "400" ]] || {
    echo "ERROR: PUT name change should be rejected"
    echo "${bad_resp}"
    return 1
  }
  echo "${bad_resp}" | grep -q "Name is immutable" || {
    echo "ERROR: Expected immutable name error from PUT"
    echo "${bad_resp}"
    return 1
  }
  # PUT rejects a changed address because address is immutable.
  bad_payload='{"name":"c1","address":"127.0.0.1:9999","servers":{},"failure-domain":9}'
  bad_resp="$(daemon_config_put "${bad_payload}")"
  [[ "$(response_code "${bad_resp}")" == "400" ]] || {
    echo "ERROR: PUT address change should be rejected"
    echo "${bad_resp}"
    return 1
  }
  echo "${bad_resp}" | grep -q "Address is immutable" || {
    echo "ERROR: Expected immutable address error from PUT"
    echo "${bad_resp}"
    return 1
  }
  # PUT rejects unknown extension listeners instead of silently accepting them.
  bad_payload='{"name":"c1","address":"127.0.0.1:9001","servers":{"unknown.example.com":{"address":"127.0.0.1:9443"}},"failure-domain":9}'
  bad_resp="$(daemon_config_put "${bad_payload}")"
  [[ "$(response_code "${bad_resp}")" == "400" ]] || {
    echo "ERROR: Unknown server should be rejected"
    echo "${bad_resp}"
    return 1
  }
  echo "${bad_resp}" | grep -q "No matching additional listener found" || {
    echo "ERROR: Expected unknown server validation error"
    echo "${bad_resp}"
    return 1
  }
  # Compatibility: the legacy daemon/servers endpoint should still be accessible.
  legacy_resp="$(curl -sS --unix-socket "${socket_path}" http://unix/core/1.0/daemon/servers)"
  [[ "$(response_code "${legacy_resp}")" == "200" ]] || {
    echo "ERROR: Legacy daemon/servers endpoint should remain available"
    echo "${legacy_resp}"
    return 1
  }

  echo "==> restart semantics"

  # Before requesting a restart, the live dqlite metadata should still report the default value.
  fd_before="$(microctl --state-dir "${test_dir}/c1" describe 127.0.0.1:9001 | yq -r '."failure-domain"')"
  [[ "${fd_before}" == "0" ]] || {
    echo "ERROR: Expected initial dqlite failure-domain to be 0, got ${fd_before}"
    return 1
  }

  # PATCH with restart=true should apply the failure-domain to the live dqlite node before returning.
  patch_resp="$(daemon_config_patch '{"failure-domain":42}' true)"
  [[ "$(response_code "${patch_resp}")" == "200" ]] || {
    echo "ERROR: PATCH with restart failed"
    echo "${patch_resp}"
    return 1
  }
  # The request should return only after the local database is back online.
  fd_after="$(microctl --state-dir "${test_dir}/c1" describe 127.0.0.1:9001 | yq -r '."failure-domain"')"
  [[ "${fd_after}" == "42" ]] || {
    echo "ERROR: Expected dqlite failure-domain to be 42 after PATCH restart, got ${fd_after}"
    return 1
  }

  config_resp="$(daemon_config_get)"
  [[ "$(json_get "${config_resp}" '.metadata."failure-domain"')" == "42" ]] || {
    echo "ERROR: GET should reflect failure-domain=42 after PATCH restart"
    echo "${config_resp}"
    return 1
  }
  # PUT with restart=true should preserve full replacement semantics and still wait for the restart.
  put_resp="$(daemon_config_put '{"name":"c1","address":"127.0.0.1:9001","failure-domain":99}' true)"
  [[ "$(response_code "${put_resp}")" == "200" ]] || {
    echo "ERROR: PUT with restart failed"
    echo "${put_resp}"
    return 1
  }
  fd_after="$(microctl --state-dir "${test_dir}/c1" describe 127.0.0.1:9001 | yq -r '."failure-domain"')"
  [[ "${fd_after}" == "99" ]] || {
    echo "ERROR: Expected dqlite failure-domain to be 99 after PUT restart, got ${fd_after}"
    return 1
  }
  # Without restart=true, PATCH persists the new value but live dqlite metadata remains unchanged.
  patch_resp="$(daemon_config_patch '{"failure-domain":7}')"
  [[ "$(response_code "${patch_resp}")" == "200" ]] || {
    echo "ERROR: PATCH without restart failed"
    echo "${patch_resp}"
    return 1
  }

  fd_live="$(microctl --state-dir "${test_dir}/c1" describe 127.0.0.1:9001 | yq -r '."failure-domain"')"
  [[ "${fd_live}" == "99" ]] || {
    echo "ERROR: Without restart, live dqlite failure-domain should still be 99, got ${fd_live}"
    return 1
  }

  config_resp="$(daemon_config_get)"
  [[ "$(json_get "${config_resp}" '.metadata."failure-domain"')" == "7" ]] || {
    echo "ERROR: GET should reflect the persisted failure-domain=7 even without restart"
    echo "${config_resp}"
    return 1
  }
  shutdown_systems
}

test_extended_endpoints() {
  new_systems 4 --heartbeat 2s

  # Bootstrap initial cluster.
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

  # Get join tokens for the other cluster members.
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")

  # Join the cluster members.
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}"
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}"
  microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}"

  # Test the extended simple endpoint.
  microctl --state-dir "${test_dir}/c1" extended simple
  for i in 1 2 3 4; do
    microctl --state-dir "${test_dir}/c1" extended simple --target "c${i}"
  done

  # Test the extended websocket endpoint.
  microctl --state-dir "${test_dir}/c1" extended websocket
  for i in 1 2 3 4; do
    microctl --state-dir "${test_dir}/c1" extended websocket --target "c${i}"
  done

  shutdown_systems
}

test_prejoin_failure() {
  echo "Testing PreJoin failure cleanup"

  # Start 3 systems (c4 will be started manually)
  new_systems 3 --heartbeat 2s

  # Bootstrap c1 and join c2, c3
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}"
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}"

  # Wait for cluster to stabilize
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]]; do
    sleep 2
  done

  echo "  -> Starting c4 with FAIL_PREJOIN=1"
  mkdir -p "${test_dir}/c4"
  FAIL_PREJOIN=1 microd --state-dir "${test_dir}/c4" --heartbeat 2s &
  microctl --state-dir "${test_dir}/c4" waitready

  # First join attempt should fail due to FAIL_PREJOIN
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")
  ! microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}" || {
    echo "ERROR: c4 join should have failed due to PreJoin hook failure"
    return 1
  }

  echo "  -> PreJoin failure triggered successfully, restarting c4 without FAIL_PREJOIN"

  # Kill c4 and restart without FAIL_PREJOIN
  microctl --state-dir "${test_dir}/c4" shutdown || true
  sleep 2

  microd --state-dir "${test_dir}/c4" --heartbeat 2s &
  microctl --state-dir "${test_dir}/c4" waitready

  # c4 should now be able to join successfully
  # (this tests that the synchronous cleanup after the failed PreJoin left the cluster state clean)
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")
  microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}" || {
    echo "ERROR: c4 should be able to join after failed PreJoin cleanup"
    return 1
  }

  # Wait for cluster to stabilize
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]]; do
    sleep 2
  done

  # Verify c4 is a voter
  [[ $(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.clustermemberlocal.name == "c4").role') == "voter" ]]

  shutdown_systems
}

test_self_deletion() {
  echo "Testing self deletion"

  new_systems 4 --heartbeat 2s

  # Bootstrap initial cluster.
  microctl --state-dir "${test_dir}/c1" init "c1" 127.0.0.1:9001 --bootstrap

  # Get join tokens for the other cluster members.
  token_c2=$(microctl --state-dir "${test_dir}/c1" tokens add "c2")
  token_c3=$(microctl --state-dir "${test_dir}/c1" tokens add "c3")
  token_c4=$(microctl --state-dir "${test_dir}/c1" tokens add "c4")

  # Join the cluster members.
  microctl --state-dir "${test_dir}/c2" init "c2" 127.0.0.1:9002 --token "${token_c2}"
  microctl --state-dir "${test_dir}/c3" init "c3" 127.0.0.1:9003 --token "${token_c3}"
  microctl --state-dir "${test_dir}/c4" init "c4" 127.0.0.1:9004 --token "${token_c4}"

  # Wait for cluster to stabilize
  while [[ -n "$(microctl --state-dir "${test_dir}/c1" cluster list -f yaml | yq '.[] | select(.role == "PENDING")')" ]]; do
    sleep 2
  done

  echo "  -> Testing self deletion of member c1"
  microctl --state-dir "${test_dir}/c1" cluster remove c1

  echo "  -> Testing c1 got reset"
  while [[ "$(microctl --state-dir "${test_dir}/c1" cluster list 2>&1)" != "Error: Database is not yet initialized" ]]; do
    sleep 2
  done

  echo "  -> Testing c1 is no longer present on the remaining cluster"
  [ "$(microctl --state-dir "${test_dir}/c2" cluster list -f csv | wc -l)" = "3" ]
  [ "$(microctl --state-dir "${test_dir}/c2" cluster list -f json | yq '.[] | select(.name == "c1")')" = "" ]

  shutdown_systems
}

# allow for running a specific set of tests
TEST_RESULT="success"
if [ "${1:-"all"}" = "all" ] || [ "${1}" = "" ]; then
  run_test misc
  run_test tokens
  run_test recover
  run_test join_token_after_cluster_formed
  run_test join_token_before_cluster_formed
  run_test daemon_config_api
  run_test extended_endpoints
  run_test membership_consistency
  run_test truststore_force_removal
  run_test parallel_joins
  run_test prejoin_failure
  run_test self_deletion
elif [ "${1}" = "recover" ]; then
  run_test recover
elif [ "${1}" = "tokens" ]; then
  run_test tokens
elif [ "${1}" = "misc" ]; then
  run_test misc
elif [ "${1}" = "join-after" ]; then
  run_test join_token_after_cluster_formed
elif [ "${1}" = "join-before" ]; then
  run_test join_token_before_cluster_formed
elif [ "${1}" = "extended" ]; then
  run_test extended_endpoints
elif [ "${1}" = "membership" ]; then
  run_test membership_consistency
elif [ "${1}" = "force-removal" ]; then
  run_test truststore_force_removal
elif [ "${1}" = "parallel-join" ]; then
  run_test parallel_joins
elif [ "${1}" = "prejoin" ]; then
  run_test prejoin_failure
elif [ "${1}" = "self-deletion" ]; then
  run_test self_deletion
elif [ "${1}" = "daemon-config" ]; then
  run_test daemon_config_api
else
  echo "Unknown test ${1}"
  TEST_RESULT="failure"
  exit 1
fi
