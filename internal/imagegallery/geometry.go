package imagegallery

// DisplayFont is an immutable rendering of a prepared revision for one text
// face and cell geometry. Commit the revision after successful activation.
type DisplayFont struct {
	FontPath, PostScript string
	Existing             bool
}
