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

package opctx_test

import gomock "go.uber.org/mock/gomock"

func NewNoOpMockDryRunnable(ctrl *gomock.Controller) *MockDryRunnable {
	mock := NewMockDryRunnable(ctrl)
	mock.EXPECT().DryRun().AnyTimes()

	return mock
}

func NoOpMockEvent(ctrl *gomock.Controller) *MockEvent {
	mockEvent := NewMockEvent(ctrl)
	mockEvent.EXPECT().SetLongRunning(gomock.Any()).AnyTimes()
	mockEvent.EXPECT().SetProgress(gomock.Any(), gomock.Any()).AnyTimes()
	mockEvent.EXPECT().End().AnyTimes()

	return mockEvent
}

func NewNoOpMockEventListener(ctrl *gomock.Controller) *MockEventListener {
	mock := NewMockEventListener(ctrl)
	mock.EXPECT().Event(gomock.Any()).AnyTimes()

	// NOTE: Because StartEvent() is variadic and can take any odd number of arguments,
	// we need to set up multiple expectations for different argument counts.
	mock.EXPECT().StartEvent(
		gomock.Any(),
	).AnyTimes().Return(NoOpMockEvent(ctrl))
	mock.EXPECT().StartEvent(
		gomock.Any(), gomock.Any(), gomock.Any(),
	).AnyTimes().Return(NoOpMockEvent(ctrl))
	mock.EXPECT().StartEvent(
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
	).AnyTimes().Return(NoOpMockEvent(ctrl))

	return mock
}
