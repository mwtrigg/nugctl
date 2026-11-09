package verify

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// nuspecDoc is the part of a .nuspec manifest the checks read
// (https://learn.microsoft.com/nuget/reference/nuspec). Element names match
// in any XML namespace, as nuspec files use several.
type nuspecDoc struct {
	Metadata struct {
		ID           string `xml:"id"`
		Version      string `xml:"version"`
		Dependencies struct {
			Groups []struct {
				TargetFramework string `xml:"targetFramework,attr"`
				Deps            []struct {
					ID      string `xml:"id,attr"`
					Version string `xml:"version,attr"`
				} `xml:"dependency"`
			} `xml:"group"`
		} `xml:"dependencies"`
	} `xml:"metadata"`
}

func parseNuspec(b []byte) (*nuspecDoc, error) {
	var d nuspecDoc
	if err := xml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Metadata.ID == "" || d.Metadata.Version == "" {
		return nil, fmt.Errorf("nuspec has no metadata id/version")
	}
	return &d, nil
}

// nuspecFromNupkg returns the bytes of the .nuspec at the root of a .nupkg.
func nuspecFromNupkg(nupkg []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(nupkg), int64(len(nupkg)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if !strings.Contains(f.Name, "/") && strings.HasSuffix(strings.ToLower(f.Name), ".nuspec") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("no .nuspec at the package root")
}
