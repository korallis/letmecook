package inference

import "context"

// Start refuses until the S3 boundary implementation is installed.
func Start(ctx context.Context, s Scope, sink ReceiptSink) (Boundary, error) {
	return nil, ErrNotImplemented
}
