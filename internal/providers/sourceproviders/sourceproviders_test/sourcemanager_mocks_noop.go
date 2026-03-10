// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// This package provides customized versions of the GoMock-generated mocks.
//
// Return values of each mock are:
// 	- For primitive types, the zero value is returned.
// 	- For complex types, another no-op mock is returned.
//
// If a mocked method would normally cause side effects like creating a file or
// starting a long-running operation, the no-op mock will not perform those actions.

package sourceproviders_test

import gomock "go.uber.org/mock/gomock"

// NewNoOpMockSourceManager creates a new instance of [MockSourceManager]
// that never errors and does not create any files on the filesystem.
func NewNoOpMockSourceManager(ctrl *gomock.Controller) *MockSourceManager {
	mock := NewMockSourceManager(ctrl)
	mock.EXPECT().FetchFiles(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mock.EXPECT().FetchComponent(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

	return mock
}
