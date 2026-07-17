package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade nugctl to the latest release",
	RunE: func(cmd *cobra.Command, args []string) error {
		current, _ := cmd.Root().Flags().GetString("version")
		if current == "" {
			current = cmd.Root().Version
		}

		fmt.Println("Checking for updates...")
		release, err := fetchLatestRelease()
		if err != nil {
			return fmt.Errorf("fetching release info: %w", err)
		}

		tag := release.TagName
		fmt.Printf("Latest: %s  Current: %s\n", tag, current)
		if tag == current || "v"+current == tag {
			fmt.Println("Already up to date.")
			return nil
		}

		assetName := assetNameForPlatform()
		assetURL := ""
		for _, a := range release.Assets {
			if a.Name == assetName {
				assetURL = a.BrowserDownloadURL
				break
			}
		}
		if assetURL == "" {
			return fmt.Errorf("no asset found for %s in release %s", assetName, tag)
		}

		fmt.Printf("Downloading %s...\n", assetName)
		self, err := os.Executable()
		if err != nil {
			return err
		}

		tmp := self + ".new"
		if err := downloadFile(assetURL, tmp); err != nil {
			return err
		}
		if err := os.Chmod(tmp, 0o755); err != nil {
			return err
		}
		if err := replaceSelf(self, tmp); err != nil {
			os.Remove(tmp)
			hint := "try with sudo?"
			if runtime.GOOS == "windows" {
				hint = "try again, or replace the binary manually while nugctl is not running"
			}
			return fmt.Errorf("replacing binary: %w (%s)", err, hint)
		}
		fmt.Printf("Updated %s → %s\n", current, tag)
		return nil
	},
}

// replaceSelf installs tmp over self, the running executable.
//
// On Windows this can't be done with a single rename: the OS won't let you
// overwrite a file that's mapped into a running process, and a
// freshly-downloaded file is often briefly locked by antivirus real-time
// scanning. replaceViaShuffle works around both.
func replaceSelf(self, tmp string) error {
	if runtime.GOOS != "windows" {
		return renameWithRetry(tmp, self)
	}
	return replaceViaShuffle(self, tmp)
}

// replaceViaShuffle installs tmp over self by moving self aside first
// (renaming an open file is allowed even though overwriting one isn't),
// moving tmp into place, and cleaning up the old binary on a best-effort
// basis. If installing tmp fails, self is restored from the moved-aside
// copy so the caller isn't left without a working binary. Each rename is
// retried briefly to ride out a transient AV lock.
func replaceViaShuffle(self, tmp string) error {
	old := self + ".old"
	os.Remove(old) // leftover from an interrupted previous upgrade
	if err := renameWithRetry(self, old); err != nil {
		return fmt.Errorf("moving current binary aside: %w", err)
	}
	if err := renameWithRetry(tmp, self); err != nil {
		renameWithRetry(old, self) // best-effort restore
		return fmt.Errorf("installing new binary: %w", err)
	}
	os.Remove(old) // best-effort cleanup; a leftover .old is harmless
	return nil
}

// renameAttempts/renameRetryDelay are vars (not consts) so tests can shrink
// them instead of a real upgrade's retry loop running at test speed.
var (
	renameAttempts   = 10
	renameRetryDelay = 200 * time.Millisecond
)

func renameWithRetry(oldpath, newpath string) error {
	var err error
	for i := 0; i < renameAttempts; i++ {
		if err = os.Rename(oldpath, newpath); err == nil {
			return nil
		}
		time.Sleep(renameRetryDelay)
	}
	return err
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease() (*githubRelease, error) {
	url := "https://api.github.com/repos/mwtrigg/nugctl/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API: %d %s", resp.StatusCode, body)
	}
	var rel githubRelease
	return &rel, json.NewDecoder(resp.Body).Decode(&rel)
}

func assetNameForPlatform() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	name := fmt.Sprintf("nugctl_%s_%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
