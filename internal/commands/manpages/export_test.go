package manpages

// WriteFileForTest lets external tests verify file failures with the actual
// application error presenter, without a production dependency on assembly.
var WriteFileForTest = writeManpageFile
