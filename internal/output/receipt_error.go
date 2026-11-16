package output

// ReceiptError reports that output was saved but its receipt could not be written.
type ReceiptError struct{ cause error }

// NewReceiptError retains the failed diagnostic write behind a fixed message.
func NewReceiptError(cause error) *ReceiptError { return &ReceiptError{cause: cause} }

func (e *ReceiptError) Error() string {
	return "The output was saved, but its receipt could not be written."
}
func (e *ReceiptError) Unwrap() error { return e.cause }
