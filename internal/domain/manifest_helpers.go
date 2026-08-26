package domain

// ManifestReady reports whether a manifest has a non-empty integrity checksum.
func ManifestReady(manifest *ReleaseManifest) bool {
	return manifest != nil && manifest.CaptionChecksum != "" && manifest.MediaChecksum != ""
}
