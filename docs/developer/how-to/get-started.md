# Getting Started

## Prerequisites

- **go 1.25+** - Required for building and running the application
- **git** - For version control and submitting changes
- **mage** - Our build automation tool

  ```bash
  # Mage is not technically required, the build tools may also be invoked
  # directly via `go run magefile.go` or `./magefile.go`
  go install github.com/magefile/mage@latest
  ```

## Initial Setup

1. Fork the repository (strongly recommended for all contributors)
2. Clone your fork locally
3. Install dependencies, e.g.:

   ```bash
   dnf install -y golang
   ```

4. Verify the build works:

   ```bash
   mage build
   ```

5. Install the built tools to your `$GOPATH/bin` directory:

   ```bash
   mage install
   ```

   After running `mage install`, the tools will be available in your `$GOPATH/bin` directory.
   Not all systems will have this directory in their `$PATH` by default, so you may need to add it.

### Azure Linux 3.0 as a development host

```bash
# These instructions work for Azure Linux (https://github.com/microsoft/azurelinux)
# While the tools should build and run on any system with golang installed, some of the runtime dependencies
# may not be available on all systems (e.g., `mock`).
tdnf install -y golang ca-certificates glibc-devel

# Install runtime requirements for the azldev tool
tdnf install -y mock dnf-utils mock-rpmautospec

git clone <URL>
cd <REPO>

# You may want to permanently add $GOPATH/bin to your $PATH via
#     echo 'export GOPATH=$(go env GOPATH)' >> $HOME/.bashrc
#     echo 'export PATH=$PATH:${GOPATH}/bin' >> $HOME/.bashrc
export GOPATH=$(go env GOPATH)
export PATH=$PATH:${GOPATH}/bin
go install github.com/magefile/mage@latest
mage build
mage install
```

## Development

Please review these follow-up documents:

- [Suggested development workflow](./dev-workflow.md).
- [Coding standards](../reference/coding-standards.md).
- [Submitting pull requests](./pull-requests.md)

## Testing

Testing can be run with `mage`, or directly using `go test`. The `mage` build system is preferred as it can also automatically run the scenario tests and update the test expectations.

```bash
# Run the unit tests
mage unit

# Run the coverage tests, producing an .html report
mage coverage

# Run scenario tests
mage scenario
```

See the [testing documentation](./testing.md) for detailed guidelines on writing and running tests.

## Troubleshooting

### Common Issues

- **Build failures**: Ensure Go version is 1.25+ and all dependencies are installed. Try running `mage build -v`. Ensure `GOPATH` is configured.
- **Test failures**: Run `mage generate` to ensure generated code is up to date
- **Lint errors**: Address specific linting issues or discuss exceptions with maintainers
- **Command not found**: Run `mage install`, ensure `GOPATH` is in your `$PATH`.

## Questions and Support

If you have any questions about contributing, please open an issue in the repository.

Thank you for contributing!
