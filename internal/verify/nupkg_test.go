package verify

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestBuildMinimalNupkg_ValidZipWithNuspecAndContentFile(t *testing.T) {
	data, err := buildMinimalNupkg("nugctl-verify-123", "1.0.0")
	if err != nil {
		t.Fatalf("buildMinimalNupkg: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("resulting bytes are not a valid zip: %v", err)
	}

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["nugctl-verify-123.nuspec"] {
		t.Errorf("expected nuspec at root, got entries %v", names)
	}
	if !names["readme.txt"] {
		t.Errorf("expected a content file, got entries %v", names)
	}
}
