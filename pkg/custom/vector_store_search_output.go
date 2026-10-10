package custom

// Empty search results do not establish the indexing state. Keep this wording
// specific to successful, untransformed search output; callers check errors first.
func vectorStoreSearchEmptyText(opts ShowJSONOpts) (string, bool) {
	if opts.Operation != "(resource) vector_stores > (method) search" ||
		opts.OutputKind != OutputPageItem || opts.Transform != "" || opts.RawOutput {
		return "", false
	}
	return "No matches returned.\nIndexing state was not checked.", true
}
