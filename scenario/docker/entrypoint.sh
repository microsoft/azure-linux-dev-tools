#!/bin/bash
set -euo pipefail

# In case some files were copied into the container after building the Dockerfile,
# make sure the work directory is entirely owned by the test user.
sudo chown -R $(id -u):$(id -g) $PWD

# Now dispatch to the real command.
exec "$@"
