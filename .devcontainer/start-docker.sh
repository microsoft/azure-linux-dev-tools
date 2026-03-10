#!/bin/bash

# Start Docker services
set -e
echo Starting Docker services...
dockerd > /var/log/docker_init.log 2>&1 &
while ! docker info 2>&1; do
    sleep 0.1
done
echo Docker ready!
