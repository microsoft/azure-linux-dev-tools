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

package rpm_test

import gomock "go.uber.org/mock/gomock"

func NewNoOpMockRPMExtractor(ctrl *gomock.Controller) *MockRPMExtractor {
	mock := NewMockRPMExtractor(ctrl)
	mock.EXPECT().Extract(gomock.Any(), gomock.Any()).AnyTimes()

	return mock
}
