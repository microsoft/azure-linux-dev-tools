// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testctx

// TestOSEnv is a test implementation of the OSEnv interface that allows tests
// to control the environment without mutating the host environment.
type TestOSEnv struct {
	workingDir string
	envVars    map[string]string
}

// Constructs a new [TestOSEnv] instance with the default working directory initialized to "/".
func NewTestOSEnv() *TestOSEnv {
	return &TestOSEnv{
		workingDir: "/",
		envVars:    make(map[string]string),
	}
}

// SetEnv sets a test environment variable on the [TestOSEnv] instance.
//
// Note that [SetEnv] is not part of the [opctx.OSEnv] interface, so it is only
// available when working with [TestOSEnv] directly. The intended usage pattern
// in tests is:
//
//	env := NewTestOSEnv()
//	env.SetEnv("KEY", "VALUE")
//	ctx := NewCtx(
//	    ctx,
//	    WithOSEnv(env),
//	)
//
// This allows tests to configure environment variables without mutating the
// real host environment while still passing an [opctx.OSEnv]-compatible value
// into [NewCtx] via [WithOSEnv].
func (env *TestOSEnv) SetEnv(key, value string) {
	env.envVars[key] = value
}

// Getwd implements the [opctx.OSEnv] interface.
func (env *TestOSEnv) Getwd() (string, error) {
	return env.workingDir, nil
}

// Chdir implements the [opctx.OSEnv] interface.
func (env *TestOSEnv) Chdir(dir string) error {
	env.workingDir = dir

	return nil
}

// IsCurrentUserMemberOf implements the [opctx.OSEnv] interface.
func (env *TestOSEnv) IsCurrentUserMemberOf(groupName string) (isMember bool, err error) {
	return true, nil
}

// LookupGroupID implements the [opctx.OSEnv] interface.
func (env *TestOSEnv) LookupGroupID(groupName string) (gid int, err error) {
	const testGroupID = 5000

	// For testing purposes, we can return a fixed group ID.
	return testGroupID, nil
}

// Getenv implements the [opctx.OSEnv] interface.
func (env *TestOSEnv) Getenv(key string) string {
	return env.envVars[key]
}
