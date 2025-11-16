package buildinfo

// Version and Date are populated at build time via -ldflags. They default to
// development values so local builds remain identifiable even without metadata.
var (
	Version = "dev"
	Date    = "unknown"
)
