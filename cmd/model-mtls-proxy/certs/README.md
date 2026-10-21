This directory intentionally contains no credentials in Git.

The production Docker build mounts `client.crt`, `client.key`, and
`gateway-ca.crt` here as BuildKit secrets while compiling the static proxy.
