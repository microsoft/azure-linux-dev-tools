# Testing guidelines

This document provides guidelines for writing and maintaining tests within the AZL Dev Preview project, with a special focus on using interface mocks via GoMock.

## Table of Contents

- [Unit Tests](#unit-tests)
- [Scenario Tests](#scenario-tests)
- [Test Utilities](#test-utilities)
- [Mocking dependencies](#mocking-dependencies)
  - [Generating Mocks](#generating-mocks)
  - [Using Mocks in Tests](#using-mocks-in-tests)
    - [Best Practices](#best-practices)
    - [Basic Usage](#basic-usage)
    - [Setting Expectations](#setting-expectations)
    - [Setting up mock return values](#setting-up-mock-return-values)
    - [Testing Error Conditions](#testing-error-conditions)
    - [Mocks side effects](#mocks-side-effects)

## Unit Tests

- **CRITICAL**: Unit tests must NOT write to the real filesystem or spawn real external processes
- Use the test context facilities (`internal/global/testctx`) for in-memory filesystem access
- Tests should not assume any files in the real host filesystem are present
- Use table-driven tests where appropriate
- Test both success and failure cases
- Use meaningful test names that describe the scenario
- Leverage `testify` for assertions

## Scenario Tests

- Use the scenario testing framework in `scenario/`
- Test realistic user workflows
- Include both positive and negative test cases
- Use `mage scenarioUpdate` when test expectations change and snapshots need updating
- Use existing test helpers in `scenario/internal/cmdtest`

## Test Utilities

- Create reusable test fixtures when appropriate
- Mock external dependencies appropriately. See [Mocking dependencies](#mocking-dependencies) for details
- The in-memory filesystem is available via `internal/global/testctx` for testing purposes

## Mocking dependencies

The AZL Dev Preview project uses [GoMock](https://github.com/golang/mock) to generate mock implementations of interfaces for testing purposes. These mocks allow for controlled testing of components that depend on interfaces without relying on concrete implementations.

Mock generation is handled through Go's built-in [`go:generate` functionality](https://pkg.go.dev/cmd/go#hdr-Generate_Go_files_by_processing_source). The project uses a pinned version of `mockgen` stored in the `tools/mockgen` directory to ensure consistent behavior across all environments.

### Generating Mocks

To generate new mocks for an interface, add a `go:generate` directive at the top of the file containing the interface. Example from `internal/global/opctx/interfaces.go`:

```go
//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -source=interfaces.go -destination=opctx_test/opctx_mocks.go -package=opctx_test --copyright_file=../../../LICENSE
```

The directive should:

1. Reference the mockgen tool using the relative path to the tool's go.mod file
2. Specify the source file containing the interfaces
3. Define the destination path for the generated mocks. Place the file inside a folder with the `_test` suffix to ensure it is ignored during test coverage reports
4. Set the package name for the generated mocks
5. Include a copyright file reference

Mocks are automatically generated as part of the build process when running any of the below commands:

```shell
mage generate
mage build
mage unit
go generate ./...
```

NOTE: `go test` will **NOT** run the `go:generate` directives.

### Using Mocks in Tests

#### Best Practices

1. **Don't overuse mocks**: Only mock interfaces that are external to the component under test
2. **Keep tests focused**: Test one unit of functionality at a time
3. **Use helper functions**: Create helper functions to set up common mock configurations
4. **Use table-driven tests**: When testing similar functionality with different inputs
5. **Follow existing patterns**: See examples in the codebase such as `internal/utils/externalcmd/externalcmd_test.go`

#### Basic Usage

Full documentation and examples for using GoMock can be found in the [GoMock documentation](https://github.com/golang/mock?tab=readme-ov-file#building-mocks). Below is a short set of usage examples from this project.

To use the generated mocks in your tests:

1. Import the mock package:

   ```go
   import (
       "go.uber.org/mock/gomock"
       "github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
   )
   ```

2. Create a controller and mock instances:

   ```go
   func TestMyFunction(t *testing.T) {
       // Create a new controller
       ctrl := gomock.NewController(t)

       // Create mock instances using the controller
       mockFileSystem := opctx_test.NewMockFileSystemFactory(ctrl)
       mockOSEnv := opctx_test.NewMockOSEnvFactory(ctrl)

       // Use the mocks in your test
       app := azldev.NewApp(mockFileSystem, mockOSEnv)

       // Set expectations, run test code, verify
   }
   ```

#### Setting Expectations

GoMock allows you to define expectations for method calls on your mock objects:

```go
// Expect DryRun() to be called any number of times and return false
mockDryRunnable.EXPECT().DryRun().AnyTimes().Return(false)

// Expect GetEventListener() to be called exactly once with any arguments
// and return a specific mock object
mockContext.EXPECT().GetEventListener().Times(1).Return(mockEventListener)
```

These expectations are automatically verified at the end of the test. If the expectations are not met, the test will fail. Similarly, if an unexpected method call is made on the mock, the test will also fail.

#### Setting up mock return values

You can configure a mock to return a specific value. This includes returning another pre-configured mock:

```go
// Must always create a controller for the mocks.
ctrl := gomock.NewController(t)

// Create a mock of the command factory interface.
mockCmdFactory := opctx_test.NewMockCommandFactory(ctrl)

// Create a mock of the command returned by the command factory.
mockCmd := opctx_test.NewMockCommand(ctrl)

// Set up the mock command's behaviour for its Run() method. Here it returns a nil error.
// The 'gomock.Any()' is an argument matcher making sure we won't fail the test regardless of the input.
mockCmd.EXPECT().Run(gomock.Any()).Return(nil)

// Set up the command factory to return the mock command when Command() is called.
mockCmdFactory.EXPECT().Command(gomock.Any()).Return(mockCmd, nil)

(...)

// Some test code using the mock command factory:
cmd, err := mockCmdFactory.Command("arbitrary input")
require.NoError(t, err)
require.Equal(t, mockCmd, cmd)

err = cmd.Run(ctx.Background())
require.NoError(t, err)

// GoMock will automatically verify that the expected calls were made on the mock.
```

#### Testing Error Conditions

Use mocks to simulate error conditions:

```go
mockCmd.EXPECT().Run(gomock.Any()).Return(errors.New("command error"))
```

#### Mocks side effects

You can define a function to be executed when a mock method is called, allowing you to simulate side effects:

```go
const testDir = "/test/dir"

// Must always create a controller for the mocks.
ctrl := gomock.NewController(t)

// Create a mock of a command we expect to run.
// The 'gomock.Any()' is an argument matcher making sure we won't fail the test regardless of the input.
testFS := afero.NewMemMapFs()
mockCmd := opctx_test.NewMockCommand(ctrl)
mockCmd.EXPECT().Run(gomock.Any()).DoAndReturn(func(_ context.Context) error {
    // Simulate some side effect. Creating a directory in this case.
    err := fileutils.MkdirAll(testFS, testDir)
    require.NoError(t, err)

    return nil
})

...
```
