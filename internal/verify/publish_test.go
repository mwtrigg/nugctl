package verify

import "testing"

func TestPublishChecks_CompliantFeed(t *testing.T) {
	r := runFull(t, newFakeFeed(false))
	wantStatuses(t, r, map[string]Status{
		"push accepts any multipart field name": StatusPass,
		"push prerelease-only package":          StatusPass,
		"relist of an absent version gives 404": StatusPass,
	})
}

func TestPublishChecks_FieldNameRequiredIsWarn(t *testing.T) {
	feed := newFakeFeed(false)
	feed.requirePackageField = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{
		"push accepts any multipart field name": StatusWarn,
		"push prerelease-only package":          StatusPass, // re-pushed as "package"
		"search excludes prerelease by default": StatusPass,
	})
}

func TestPublishChecks_RelistAbsentSucceedsFails(t *testing.T) {
	feed := newFakeFeed(false)
	feed.relistAbsentOK = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{"relist of an absent version gives 404": StatusFail})
}

func TestPublishChecks_NoRelistWarns(t *testing.T) {
	feed := newFakeFeed(true)
	feed.noRelist = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{"relist of an absent version gives 404": StatusWarn})
}
