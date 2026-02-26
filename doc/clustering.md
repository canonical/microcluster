# Initialize a Microcluster

## Bootstrap the first member

The first member can be bootstrapped with a request on the local unix socket using:

```go
m, err := microcluster.App(microcluster.Args{StateDir: "/path/to/state"})
if err != nil {
    // ...
}

// Cluster member name, usually hostname.
memberName := "member1"

// Cluster member address and port.
memberAddress := "10.0.0.101:8000"

// Config to pass into PreInit and PostBootstrap lifecycle hooks.
cfg := map[string]string{"key": "val"}

// Bootstrap the cluster member.
err = m.NewCluster(ctx, memberName, memberAddress, cfg)
if err != nil {
    // ...
}
```

## Issue a join token

A join token allows a new cluster member with a given name to join the cluster. A join token is a `base64` encoded string that must be supplied with a join request.

Join tokens can only be issued over the local unix socket of an initialized cluster member. To issue a token, a cluster member name and token expiry must be provided. The given name must be present in the `server.crt` Subject Alternative Names (SAN) of the joining cluster member.


```go
m, err := microcluster.App(microcluster.Args{StateDir: "/path/to/state"})
if err != nil {
    // ...
}

// Cluster member name of the joiner. It will be verified against the server.crt SAN of the joiner when the token is used.
joinerName := "member2"

// How long the join token will be valid before its record is deleted.
expireAfter := 3 * time.Hour

// Issue the join token.
token, err := m.NewJoinToken(ctx, joinerName, expireAfter)
if err != nil {
    // ...
}
```

## Join an existing cluster with a join token

Use an existing join token to add a new member to the cluster. This request can only be initiated over the local unix socket of the joiner.

Before triggering the [PostJoin](https://github.com/canonical/microcluster/blob/v3/microcluster/types/hooks.go#L68) lifecycle hook, all previously existing cluster members will concurrently run their [OnNewMember](https://github.com/canonical/microcluster/blob/v3/microcluster/types/hooks.go#L84) lifecycle hooks.

```go
m, err := microcluster.App(microcluster.Args{StateDir: "/path/to/state"})
if err != nil {
    // ...
}

// Cluster member name, usually hostname. This must match the name provided when the join token was issued.
memberName := "member2"

// Cluster member address and port.
memberAddress := "10.0.0.102:8000"

// Config to pass into PreInit and PostJoin lifecycle hooks.
cfg := map[string]string{"key": "val"}

// Request to join a cluster with the provided join token.
err = m.JoinCluster(ctx, memberName, memberAddress, token, cfg)
if err != nil {
    // ...
}
```

## Remove a cluster member

A cluster member can be removed using only its name. Any member can request to remove itself or any other member from the cluster.

In order to ensure database quorum is maintained, cluster members with a `PENDING` role cannot be removed from a cluster if there are fewer than 2 voters present.

If `force=true`, errors encountered when attempting to reset the removed member back to an un-initialized state will be ignored. This should be used if the cluster member is no longer reachable by other members.

* The [PreRemove](https://github.com/canonical/microcluster/blob/v3/microcluster/types/hooks.go#L75) hook is executed on the to-be-removed member before it is removed from the database.
* The [PostRemove](https://github.com/canonical/microcluster/blob/v3/microcluster/types/hooks.go#L78) hook is executed on all remaining members after the to-be-removed member is removed from the database.

```go
m, err := microcluster.App(microcluster.Args{StateDir: "/path/to/state"})
if err != nil {
    // ...
}

memberName := "member2"
force := false
err := m.RemoveClusterMember(ctx, memberName, "", force)
if err != nil {
    // ...
}
```

In the case of an inconsistency between the internal truststore, dqlite and the database, you can attempt a member removal by supplying the member's address, which allows Microcluster to determine the member if it can no longer be looked up by its name alone:

```go
force := true
memberName := "member2"
memberAddress := "10.0.0.102:8000"

err := m.RemoveClusterMember(ctx, memberName, memberAddress, force)
if err != nil {
    // ...
}
```
