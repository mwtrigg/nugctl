# feed verify: known coverage gaps

`nugctl feed verify` is used as a NuGet v3 conformance oracle, so anything it
does not check is something a half-implemented feed can get wrong and still
pass. These are the gaps found so far (from testing it against Barn, a NuGet
v3 feed that was being brought up to the spec). Each is written as an issue:
what is unchecked, and a suggested check. Spec references are to
learn.microsoft.com/nuget/api.

Fixed already: standalone registration leaf `catalogEntry` as a URL string
(both shapes are accepted), and relist (`feed verify --push` now relists on
feeds that unlist on DELETE).

## TODO

- [ ] **Original id casing in JSON bodies.** A feed that lowercases ids in
  registration and search passes every check, yet breaks PowerShell module
  imports on case-sensitive filesystems (PSResourceGet saves to the id it
  reads). Check: push a mixed-case id; the registration `catalogEntry.id`
  and search `data[].id` must equal the `.nuspec` id byte for byte.
- [ ] **Required registration fields.** Page `@id`, `lower`, `upper`; leaf
  `@id`; catalogEntry `@id`; `parent` on separately fetched pages. A feed
  missing all of them passes today.
- [ ] **Non-inlined registration pages.** When a page has no `items`, fetch
  its `@id` and validate the page (the readonly checks only look at inlined
  leaves).
- [ ] **Dependencies.** Push a package with a dependency group and assert
  `catalogEntry.dependencyGroups[].dependencies[]` (`id`, `range`,
  `registration`). A feed that serves no dependency groups passes today,
  and clients then install no dependencies.
- [ ] **Unlisted versions stay in registration.** After unlisting on a feed
  that unlists rather than deletes, registration must still list the
  version with `listed:false`, and the flat container must still list it.
- [ ] **Push body: first multipart part, any name.** nugctl always names the
  part `package`, so a feed that requires that name passes. Optional: push
  once with a different form field name.
- [ ] **Relist of an absent version.** PackagePublish says `POST` relist of a
  version that does not exist gives 404. Not checked (the relist check only
  covers the happy path, and only on feeds that unlist on DELETE).
- [ ] **gzip on RegistrationsBaseUrl/3.4.0 and 3.6.0.** `Accept-Encoding:
  gzip` should get `Content-Encoding: gzip`. Unchecked.
- [ ] **Proxy / read-only feeds.** `feed verify` against a proxying feed
  would catch a broken service index there. Document a "verify a read-only
  or proxy feed with `--package <id>`" recipe in the README.

## Done

- [x] **Service index type strings** (#8). `client type string: <resource>`
  warns when none of a resource's advertised `@type` values is one NuGet's
  client looks up by exact match (list from NuGet.Client `ServiceTypes.cs`).
  Note `SearchQueryService/3.0.0-rc` is a documented alias but is *not* on
  that list.
- [x] **Search `versions[]`, `prerelease`, `semVerLevel`, autocomplete** (#5).
  Readonly: `search result shape (versions[])`, `autocomplete: package ids`,
  `autocomplete: package versions`. With `--push`: a prerelease-only and a
  SemVer 2.0.0-only package, and `search excludes prerelease by default`,
  `search excludes SemVer 2.0.0 without semVerLevel`, plus the same two for
  autocomplete when it is advertised. nugctl's own searches now send
  `semVerLevel=2.0.0`, as NuGet's client does.
- [x] **Flat container `.nuspec` and version normalization** (#6). Readonly:
  `flat container .nuspec download`. With `--push`: a `1.00.0.0` package and
  `version normalized: flat container` / `: registration`,
  `download via normalized version`, `nuspec download matches pushed
  package` (re-serialized manifest warns), `duplicate push of an equivalent
  version` (409 passes; an accepted replace that leaves one version warns),
  and `unlisted version stays in flat container` on feeds that unlist.
