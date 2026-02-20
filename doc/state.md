# State directory

All Microcluster information is stored and looked up in the state directory specified in `microcluster.Args`.

## Contents

Name              | Description
:---              | :----
`control.socket`  | Internal Microcluster unix socket used for communication between the client and daemon
`daemon.yaml`     | Daemon information (member name, address, additional servers, failure domain)
`cluster.crt/key` | Certificate keypair used for dqlite traffic and Core API
`server.crt/key`  | Certificate keypair used for member identification and intra-cluster authentication
`truststore`      | Directory containing local `yaml` record of cluster members
`database`        | Directory containing all `dqlite` database related information
`certificates`    | Directory containing dedicated certificates for additional servers, keyed by the server name
