package admin

// Expose mechanics only to external tests that also exercise assembly policy.
// Production callers use registration and credential validation.
var (
	TestRequestOptions = adminSetupRequestOptions
	TestVerifySetup    = verifyAdminSetup
	TestSignalContext  = adminSetupSignalContext
	TestCanceled       = adminSetupCanceled
	TestTerminated     = errAdminSetupTerminated
)
