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
- [ ] **Search: `versions[]`, `prerelease`, `semVerLevel`.** Assert
  `data[].versions[]` exists with `@id`, `version` and `downloads`; that a
  prerelease-only package is absent without `prerelease=true`; and that a
  SemVer 2.0.0 version (`1.0.0-rc.1`) is absent without `semVerLevel=2.0.0`.
- [ ] **Unlisted versions stay in registration.** After unlisting on a feed
  that unlists rather than deletes, registration must still list the
  version with `listed:false`, and the flat container must still list it.
- [ ] **Flat-container `.nuspec` download** (`{id}/{v}/{id}.nuspec`) is
  spec-defined and unchecked.
- [ ] **Version normalization.** Push `1.00.0.0`; expect the flat container
  to list `1.0.0`, a push of `1.0` to get 409, and a download via `1.0.0` to
  work.
- [ ] **Push body: first multipart part, any name.** nugctl always names the
  part `package`, so a feed that requires that name passes. Optional: push
  once with a different form field name.
- [ ] **Relist of an absent version.** PackagePublish says `POST` relist of a
  version that does not exist gives 404. Not checked (the relist check only
  covers the happy path, and only on feeds that unlist on DELETE).
- [ ] **gzip on RegistrationsBaseUrl/3.4.0 and 3.6.0.** `Accept-Encoding:
  gzip` should get `Content-Encoding: gzip`. Unchecked.
- [ ] **Service index type strings.** "resource present" matches by prefix,
  so a feed advertising only a type no Microsoft client looks up passes.
  Optional: warn when none of the exact strings clients use is present (for
  example `SearchQueryService/3.0.0-rc`, `RegistrationsBaseUrl/3.6.0`).
- [ ] **Proxy / read-only feeds.** `feed verify` against a proxying feed
  would catch a broken service index there. Document a "verify a read-only
  or proxy feed with `--package <id>`" recipe in the README.
