FROM mcr.microsoft.com/azurelinux/base/core:3.0@sha256:bd88359666e3f20296e7fe639a480dfd3c3c8eba299b21f26e4560353e21a656
SHELL ["/bin/bash", "-c"]

ARG UID
ARG GID
ARG WORK_DIR

ENV UID=${UID}
ENV GID=${GID}

LABEL name="azldev_scenario_testing"

# Install early dependencies
#   * We require shadow-utils to be able to manage users and groups
RUN tdnf -y install shadow-utils

# Create mock group and test user up-front, using the same UID and GID as the host's user so it lines up.
RUN groupadd -f -g "${GID}" mock && \
    useradd -u "${UID}" -g "${GID}" -m testuser && \
    mkdir -p /etc/sudoers.d && \
    echo "testuser ALL=(ALL) NOPASSWD: ALL" >/etc/sudoers.d/testuser

# Install additional dependencies
#   * We require ca-certificates to be able to interact with standard https endpoints
#   * We require mock to build packages
#   * We require mock-rpmautospec to install rpmautospec support in mock
#   * We require sudo to run commands as root after dropping to test user
RUN tdnf -y install \
    ca-certificates \
    git \
    mock \
    mock-rpmautospec \
    moby-containerd \
    moby-engine \
    docker-cli \
    sudo

COPY entrypoint.sh /usr/local/bin/entrypoint.sh

# Set the default user
USER testuser:mock

# Switch to the well-known work directory that the test infrastructure will
# copy files into.
WORKDIR ${WORK_DIR}

# Set up an entry point script that will have an opportunity to update file
# ownership, etc. before running the actual command.
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
