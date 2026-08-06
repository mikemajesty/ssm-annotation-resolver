package reconciler

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-logr/logr"
)

func TestExtractParameterPathFromEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantPath  string
		expectErr bool
	}{
		{
			name:     "detail.name",
			body:     `{"detail":{"name":"/app/db/password"}}`,
			wantPath: "/app/db/password",
		},
		{
			name:     "detail.requestParameters.name",
			body:     `{"detail":{"requestParameters":{"name":"/app/api/key"}}}`,
			wantPath: "/app/api/key",
		},
		{
			name:      "missing path",
			body:      `{"detail":{"other":"value"}}`,
			expectErr: true,
		},
		{
			name:      "invalid json",
			body:      `{"detail":`,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := extractParameterPathFromEvent(tt.body)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil (path=%q)", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantPath {
				t.Fatalf("unexpected path: got %q want %q", got, tt.wantPath)
			}
		})
	}
}

func TestNewRunner_ClampsAndNormalizesOptions(t *testing.T) {
	t.Parallel()

	r := NewRunner(RunnerOptions{
		Log:                logr.Discard(),
		SQSQueueURL:        "  https://sqs.us-east-1.amazonaws.com/123/demo  ",
		SQSWaitTimeSeconds: 0,
		SQSMaxMessages:     999,
	})

	if r.sqsQueueURL != "https://sqs.us-east-1.amazonaws.com/123/demo" {
		t.Fatalf("expected trimmed queue URL, got %q", r.sqsQueueURL)
	}
	if r.sqsWaitTimeSeconds != 20 {
		t.Fatalf("expected default wait time 20, got %d", r.sqsWaitTimeSeconds)
	}
	if r.sqsMaxMessages != 10 {
		t.Fatalf("expected clamped max messages 10, got %d", r.sqsMaxMessages)
	}
}

func TestProcessQueueBatch_ReconcilesAndDeletesValidMessages(t *testing.T) {
	t.Parallel()

	fakeClient := &fakeSQSClient{
		receiveOutput: &sqs.ReceiveMessageOutput{
			Messages: []sqstypes.Message{
				{
					Body:          strPtr(`{"detail":{"name":"/app/first"}}`),
					ReceiptHandle: strPtr("rh-1"),
				},
				{
					Body:          strPtr(`{"detail":{"requestParameters":{"name":"/app/second"}}}`),
					ReceiptHandle: strPtr("rh-2"),
				},
			},
		},
	}

	r := newRunnerWithSQSClient(RunnerOptions{
		Log:         logr.Discard(),
		SQSQueueURL: "https://sqs.us-east-1.amazonaws.com/123/demo",
	}, fakeClient)

	var reconciledPaths []string
	r.reconcileByParameterPathFunc = func(_ context.Context, parameterPath string) error {
		reconciledPaths = append(reconciledPaths, parameterPath)
		return nil
	}

	if err := r.processQueueBatch(context.Background()); err != nil {
		t.Fatalf("processQueueBatch returned error: %v", err)
	}

	if !slices.Equal(reconciledPaths, []string{"/app/first", "/app/second"}) {
		t.Fatalf("unexpected reconciled paths: %#v", reconciledPaths)
	}
	if !slices.Equal(fakeClient.deletedReceiptHandles, []string{"rh-1", "rh-2"}) {
		t.Fatalf("unexpected deleted receipt handles: %#v", fakeClient.deletedReceiptHandles)
	}
}

func TestProcessQueueBatch_DropsInvalidMessageAndDeletesIt(t *testing.T) {
	t.Parallel()

	fakeClient := &fakeSQSClient{
		receiveOutput: &sqs.ReceiveMessageOutput{
			Messages: []sqstypes.Message{
				{
					Body:          strPtr(`{"detail":{"other":"value"}}`),
					ReceiptHandle: strPtr("rh-invalid"),
				},
			},
		},
	}

	r := newRunnerWithSQSClient(RunnerOptions{
		Log:         logr.Discard(),
		SQSQueueURL: "https://sqs.us-east-1.amazonaws.com/123/demo",
	}, fakeClient)

	reconcileCalls := 0
	r.reconcileByParameterPathFunc = func(_ context.Context, _ string) error {
		reconcileCalls++
		return nil
	}

	if err := r.processQueueBatch(context.Background()); err != nil {
		t.Fatalf("processQueueBatch returned error: %v", err)
	}

	if reconcileCalls != 0 {
		t.Fatalf("expected zero reconcile calls, got %d", reconcileCalls)
	}
	if !slices.Equal(fakeClient.deletedReceiptHandles, []string{"rh-invalid"}) {
		t.Fatalf("unexpected deleted receipt handles: %#v", fakeClient.deletedReceiptHandles)
	}
}

func TestProcessQueueBatch_StopsOnReconcileErrorWithoutDeletingMessage(t *testing.T) {
	t.Parallel()

	fakeClient := &fakeSQSClient{
		receiveOutput: &sqs.ReceiveMessageOutput{
			Messages: []sqstypes.Message{
				{
					Body:          strPtr(`{"detail":{"name":"/app/fail"}}`),
					ReceiptHandle: strPtr("rh-fail"),
				},
			},
		},
	}

	r := newRunnerWithSQSClient(RunnerOptions{
		Log:         logr.Discard(),
		SQSQueueURL: "https://sqs.us-east-1.amazonaws.com/123/demo",
	}, fakeClient)

	wantErr := errors.New("reconcile failed")
	r.reconcileByParameterPathFunc = func(_ context.Context, _ string) error {
		return wantErr
	}

	err := r.processQueueBatch(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected reconcile error to be returned, got: %v", err)
	}
	if len(fakeClient.deletedReceiptHandles) != 0 {
		t.Fatalf("expected no message deletion on reconcile error, got %#v", fakeClient.deletedReceiptHandles)
	}
}

type fakeSQSClient struct {
	receiveOutput *sqs.ReceiveMessageOutput
	receiveErr    error
	deleteErr     error

	deletedReceiptHandles []string
}

func (f *fakeSQSClient) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if f.receiveErr != nil {
		return nil, f.receiveErr
	}
	if f.receiveOutput == nil {
		return &sqs.ReceiveMessageOutput{}, nil
	}

	return f.receiveOutput, nil
}

func (f *fakeSQSClient) DeleteMessage(_ context.Context, params *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	if params != nil && params.ReceiptHandle != nil {
		f.deletedReceiptHandles = append(f.deletedReceiptHandles, *params.ReceiptHandle)
	}

	return &sqs.DeleteMessageOutput{}, nil
}

func strPtr(v string) *string {
	return &v
}
