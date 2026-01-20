# Cluster member states

Cluster member states are determinations about the status of a cluster member when making a request to `GET /core/1.0/cluster` or [client.GetClusterMembers](https://github.com/canonical/microcluster/blob/4d80df396e335bf26f9895956e846e082bb8f624/internal/rest/client/cluster.go#L40-L49). These states are not persisted.

State         | Description
:---          | :----
ONLINE        | Cluster member responded to `GET core/1.0/ready` (ReadyChan is closed)
UNREACHABLE   | Default status. Returned if the member could not be reached, or if ReadyChan is still open
NOT TRUSTED   | Cluster member was not found in the local truststore
NOT FOUND     | Cluster member was not found in `core_cluster_members`
UPGRADING     | Version mismatch in `core_cluster_members`, but this member’s version is NOT behind
NEEDS UPGRADE | Version mismatch in `core_cluster_members`, and this member’s version is behind

# Database states

Database states represent the current initialization stage of the Dqlite database. They are mapped to error messages returned from the Microcluster API if database access is attempted but the database is unavailable.

State message                      | Description
:---                               | :----
Database is online                 | Database is online and fully available
Database is waiting for an upgrade | Database is in the process of determining if it needs to wait for a schema/API update, or is currently blocked on one.
Database is still starting         | Database is in the middle of initializing, before checking for updates
Database is not yet initialized    | Default status when the database (and daemon) is uninitialized
Database is offline                | Set when the database is explicitly turned off, such as if `db.Stop` is called or an error is detected.

# Dqlite roles

The current Dqlite role is stored in the `dqlite_role` column of `core_cluster_member_roles`. It follows typical
Dqlite roles (`voter`, `standby`, `spare`) with the addition of `PENDING` for systems that have recently joined
the cluster, but have not yet received a heartbeat through the Dqlite roles adjustment hook. At least 1
non-`PENDING` cluster member must be present to remove other members. Once a heartbeat detects that a `PENDING`
member exists in Dqlite, it will assign it the correct role.
