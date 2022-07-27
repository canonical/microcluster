# Example MicroCluster Daemon and CLI

This is an example package containing a MicroCluster daemon command (`microd`) and a control command (`microctl`) to be
used as a template for creating projects with MicroCluster.

In addition to having examples for the built-in MicroCluster API, this example shows how MicroCluster can be extended
with additional endpoints and schema versions.

The daemon can be started with `microd` and is controlled by `microctl`.

## Building
`make`

## Running
This starts up three daemons at the given addresses.
```bash
microd --state-dir /path/to/state/dir1 --address 127.0.0.1:9001 &
microd --state-dir /path/to/state/dir2 --address 127.0.0.1:9002 &
microd --state-dir /path/to/state/dir3 --address 127.0.0.1:9003 &
```

## Starting dqlite
```bash
# Wait for the daemon to finish setup.
microctl --state-dir /path/to/state/dir1 waitready

# Bootstrap the first node to start a new cluster.
microctl --state-dir /path/to/state/dir1 init --bootstrap

# Get some join tokens from the new cluster. These are deleted after use.
token_node2=$(microctl --state-dir /path/to/state/dir1 secrets /path/to/state/dir2/server.crt)
token_node3=$(microctl --state-dir /path/to/state/dir1 secrets /path/to/state/dir3/server.crt)

# Join the dqlite cluster.
microctl --state-dir /path/to/state/dir2 init --token ${token_node2}
microctl --state-dir /path/to/state/dir3 init --token ${token_node3}

# The cluster is now up and running!
```

## Interacting with the cluster
* List info on all cluster members
```bash
microctl --state-dir /path/to/state/dir1 cluster
+------+----------------+-------+------------------------------------------------------------------+--------+
| NAME |    ADDRESS     | ROLE  |                           CERTIFICATE                            | STATUS |
+------+----------------+-------+------------------------------------------------------------------+--------+
| dir1 | 127.0.0.1:9001 | voter | -----BEGIN CERTIFICATE-----                                      | ONLINE |
|      |                |       | MIIB+jCCAYCgAwIBAgIQAJ6RWpgzHgDp2zjd0DMqBjAKBggqhkjOPQQDAzAxMRww |        |
|      |                |       | GgYDVQQKExNsaW51eGNvbnRhaW5lcnMub3JnMREwDwYDVQQDDAhyb290QGJhdzAe |        |
|      |                |       | Fw0yMjA3MTEyMjE1MTVaFw0zMjA3MDgyMjE1MTVaMDExHDAaBgNVBAoTE2xpbnV4 |        |
|      |                |       | Y29udGFpbmVycy5vcmcxETAPBgNVBAMMCHJvb3RAYmF3MHYwEAYHKoZIzj0CAQYF |        |
|      |                |       | K4EEACIDYgAEH/zhSgQz98rt+lfBBqwHumRvzLrgVB5zVejKNGdVRF3PYVUxQ4hS |        |
|      |                |       | ekoaOSdUJYkevtlTcycIYmspCW+ItLSO+eVb/M8K9C4RIUf7kQiH50VgEE1TVrdj |        |
|      |                |       | lSIZT97Hogmyo10wWzAOBgNVHQ8BAf8EBAMCBaAwEwYDVR0lBAwwCgYIKwYBBQUH |        |
|      |                |       | AwEwDAYDVR0TAQH/BAIwADAmBgNVHREEHzAdggNiYXeHBH8AAAGHEAAAAAAAAAAA |        |
|      |                |       | AAAAAAAAAAEwCgYIKoZIzj0EAwMDaAAwZQIxAJ+qccU1y0hK8Zwhr98RIeGy4Pax |        |
|      |                |       | XzSgQ2yLmMAJHGPlky/ST6DGBk8G3234QccD2wIwUYnqzSQe1E4j7V9klf3eZFzy |        |
|      |                |       | rEHdUWNDQN8mzk31Qu+nq6G6O0MH34uhS/s3PYtA                         |        |
|      |                |       | -----END CERTIFICATE-----                                        |        |
|      |                |       |                                                                  |        |
+------+----------------+-------+------------------------------------------------------------------+--------+
| dir2 | 127.0.0.1:9002 | voter | -----BEGIN CERTIFICATE-----                                      | ONLINE |
|      |                |       | MIIB/DCCAYGgAwIBAgIRAPu6nctiLcTIwA4/rekYFVswCgYIKoZIzj0EAwMwMTEc |        |
|      |                |       | MBoGA1UEChMTbGludXhjb250YWluZXJzLm9yZzERMA8GA1UEAwwIcm9vdEBiYXcw |        |
|      |                |       | HhcNMjIwNzExMjIxNTE2WhcNMzIwNzA4MjIxNTE2WjAxMRwwGgYDVQQKExNsaW51 |        |
|      |                |       | eGNvbnRhaW5lcnMub3JnMREwDwYDVQQDDAhyb290QGJhdzB2MBAGByqGSM49AgEG |        |
|      |                |       | BSuBBAAiA2IABFwucU02L0giAC/zYmnKddxs3NGlmHnBNzcwljq49gTu5R7W8bdJ |        |
|      |                |       | zFcbXIwCeVjQyUBIcNfcvneEQNzwwa8umHXE0SUtHwalDuP9Nm8cWpdKME31ijVZ |        |
|      |                |       | 8BOTBRgRJdau7KNdMFswDgYDVR0PAQH/BAQDAgWgMBMGA1UdJQQMMAoGCCsGAQUF |        |
|      |                |       | BwMBMAwGA1UdEwEB/wQCMAAwJgYDVR0RBB8wHYIDYmF3hwR/AAABhxAAAAAAAAAA |        |
|      |                |       | AAAAAAAAAAABMAoGCCqGSM49BAMDA2kAMGYCMQCG8YiNbjq4dmBG+vBQbFboliGh |        |
|      |                |       | RVCRXtBFqWTzrwvgY2UqARl30n6gnQBjhrvf/uYCMQDB48u2GAV839SrmGP+lqu3 |        |
|      |                |       | oEwsaHRPVupOofm4FTIjqU7YvvZHSx1btzJ+1RTwqCA=                     |        |
|      |                |       | -----END CERTIFICATE-----                                        |        |
|      |                |       |                                                                  |        |
+------+----------------+-------+------------------------------------------------------------------+--------+
| dir3 | 127.0.0.1:9003 | voter | -----BEGIN CERTIFICATE-----                                      | ONLINE |
|      |                |       | MIIB+jCCAYGgAwIBAgIRALG9hyuIjrf3pbbC4sbAVdswCgYIKoZIzj0EAwMwMTEc |        |
|      |                |       | MBoGA1UEChMTbGludXhjb250YWluZXJzLm9yZzERMA8GA1UEAwwIcm9vdEBiYXcw |        |
|      |                |       | HhcNMjIwNzExMjIxNTE2WhcNMzIwNzA4MjIxNTE2WjAxMRwwGgYDVQQKExNsaW51 |        |
|      |                |       | eGNvbnRhaW5lcnMub3JnMREwDwYDVQQDDAhyb290QGJhdzB2MBAGByqGSM49AgEG |        |
|      |                |       | BSuBBAAiA2IABL8BgZJiKqU6QJXcg96ygwJ27HEMcxr5t5brVXEImX2AHFp3CuGW |        |
|      |                |       | Hj5+QGB1GfI87Cfq7L1kLFa6cl9DB/RHaoRUc9snHCqTJQhVyGTkaNKEwikFM7pZ |        |
|      |                |       | FW61iKopJhBQ86NdMFswDgYDVR0PAQH/BAQDAgWgMBMGA1UdJQQMMAoGCCsGAQUF |        |
|      |                |       | BwMBMAwGA1UdEwEB/wQCMAAwJgYDVR0RBB8wHYIDYmF3hwR/AAABhxAAAAAAAAAA |        |
|      |                |       | AAAAAAAAAAABMAoGCCqGSM49BAMDA2cAMGQCMAVfZoAroiRSXTvaaGqOLX/158Is |        |
|      |                |       | Vk0M9AMLxViq0PkM2mlvA6lyRCAHhIkTaUtdxgIwDHDS4PNtXOcEUo97lq0hkTqt |        |
|      |                |       | JpmhZTvkvI8X4GLYU2KxuHR3+d3G1rePjErWrWle                         |        |
|      |                |       | -----END CERTIFICATE-----                                        |        |
|      |                |       |                                                                  |        |
+------+----------------+-------+------------------------------------------------------------------+--------+
```
* Perform an SQL query
```bash
microctl --state-dir /path/to/state/dir1 sql "select name,address,schema,heartbeat from cluster_members"
# Note that the schema version is 3, because this example has extended the schema with two additional updates.
+------+----------------+--------+--------------------------------+
| name |    address     | schema |           heartbeat            |
+------+----------------+--------+--------------------------------+
| dir1 | 127.0.0.1:9001 | 3      | 2022-07-11T22:16:57.919512135Z |
| dir2 | 127.0.0.1:9002 | 3      | 2022-07-11T22:16:57.983345241Z |
| dir3 | 127.0.0.1:9003 | 3      | 2022-07-11T22:16:57.978355008Z |
+------+----------------+--------+--------------------------------+
```
* Perform an extended API interaction
```bash
microctl --state-dir /path/to/state/dir2 extended 127.0.0.1:9001
cluster member at address "127.0.0.1:9002" received message "Testing 1 2 3..." from cluster member at address "127.0.0.1:9001"
cluster member at address "127.0.0.1:9003" received message "Testing 1 2 3..." from cluster member at address "127.0.0.1:9001"
```
* Perform an SQL query on an extended schema table
```bash
microctl --state-dir /path/to/state/dir1 sql "insert into extended_table (key, value) values ('some_key', 'some_value')"
Rows affected: 1
microctl --state-dir /path/to/state/dir1 sql "select * from extended_table"
+----+----------+------------+
| id |   key    |   value    |
+----+----------+------------+
| 1  | some_key | some_value |
+----+----------+------------+
```
