# nugctl

A CLI for managing packages on [BaGetter](https://github.com/bagetter/BaGetter) and other
NuGet v3-compatible feeds — search, push, pull, unlist, and deprecate packages without
leaving the terminal.

## Install

Download a release binary from the [releases page](https://github.com/mwtrigg/nugctl/releases),
or build from source:

```sh
go build -o nugctl .
```

`nugctl` is a single static binary — no runtime dependencies.

Once installed, keep it current with:

```sh
nugctl upgrade
```

## Quick start

```sh
# Add a profile (prompts for anything you don't pass as a flag)
nugctl auth login --name prod --url https://nuget.example.com/v3/index.json --api-key XXXX

# Search
nugctl package search newtonsoft --take 50

# Push / pull
nugctl package push ./bin/MyLib.1.2.3.nupkg
nugctl package pull MyLib --version 1.2.3 --output-dir ./out

# Inspect the feed itself
nugctl feed info
```

## Profiles

`nugctl` stores one or more named feed profiles in `~/.config/nugctl/config.yml`.
Each profile has a URL, an optional API key, and an optional insecure flag.

```sh
nugctl auth login --name prod  --url https://nuget.example.com/v3/index.json --api-key XXXX
nugctl auth login --name local --url http://localhost:5555/v3/index.json          # unauthenticated
nugctl auth login --name dev   --url https://feed.internal/v3/index.json --insecure  # self-signed cert

nugctl profile list
nugctl profile use prod
nugctl profile delete dev
```

Leave `--api-key` blank (or don't pass it) for anonymous/unauthenticated feeds.

## Configuring a connection

Every connection setting can be set three ways, in this order of precedence:

**CLI flag > environment variable > profile (config file) > default**

| Setting        | Flag         | Env var             | Profile field |
|----------------|--------------|----------------------|---------------|
| Active profile | `--profile`, `-p` | `NUGCTL_PROFILE` | `current_profile` |
| Feed URL       | `--url`      | `NUGCTL_URL`         | `url` |
| API key        | `--api-key`  | `NUGCTL_API_KEY`     | `api_key` |
| Skip TLS verify| `--insecure` | `NUGCTL_INSECURE`    | `insecure` |

This means a single flag or env var lets you override a profile for one call without
editing the config file — handy in CI, or for pointing at a feed with a self-signed cert:

```sh
nugctl feed info --url https://feed.internal/v3/index.json --insecure
NUGCTL_URL=https://feed.internal/v3/index.json NUGCTL_INSECURE=true nugctl feed info
```

`--insecure` skips TLS certificate verification (equivalent to `curl -k`) — use it for
self-signed or internally-issued certs, not for feeds you don't trust.

## Commands

| Command | Description |
|---|---|
| `nugctl auth login` | Add or update a profile with feed credentials |
| `nugctl auth logout` | Remove the API key from a profile |
| `nugctl profile list` | List configured profiles |
| `nugctl profile use <name>` | Set the active profile |
| `nugctl profile delete <name>` | Delete a profile |
| `nugctl feed info` | Show the feed's service index and capabilities |
| `nugctl package search <query>` | Search for packages |
| `nugctl package list --id <id>` | List all versions of a package |
| `nugctl package info <id>` | Show package metadata |
| `nugctl package push <file.nupkg>` | Push a package to the feed |
| `nugctl package pull <id>` | Download a package from the feed |
| `nugctl package unlist <id>` | Unlist (or hard-delete) a package version |
| `nugctl package deprecate <id>` | Mark a package version as deprecated |
| `nugctl package undeprecate <id>` | Clear deprecation from a package version |
| `nugctl upgrade` | Upgrade nugctl to the latest release |

`package` can also be invoked as `pkg` or `nupkg`. Run any command with `--help` for its
full flag list and examples, e.g. `nugctl package search --help`.

## Output

Every command supports `-o table|json|yaml` (default `table`). Use `--properties` to
add specific columns to table output, or `--all-properties` / `-A` to include everything
(this also switches the default format to YAML):

```sh
nugctl package search json --properties authors,tags
nugctl feed info --all-properties
```

## Protocol support

`nugctl` speaks the **NuGet v3 protocol** (JSON service index + JSON resources) — this
covers BaGetter, NuGet.org, Azure Artifacts, and most modern feeds. It does not currently
support v2-only OData/Atom XML feeds (e.g. the Chocolatey community repository).

## Shell completion

```sh
nugctl completion bash|zsh|fish|powershell
```

## License

MIT — see [LICENSE](LICENSE).
