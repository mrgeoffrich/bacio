---
name: release-bacio
description: Cut a new release of bacio. Pre-flights the working tree, picks a semver version, tags and pushes — at which point the .github/workflows/release.yml workflow on GitHub takes over: it cross-compiles binaries for Windows amd64, Linux amd64+arm64, and macOS amd64+arm64, packages them with a sha256 checksums file, and publishes a GitHub release with the assets attached. Use this whenever the user says "release bacio", "cut a release", "publish a new version of bacio", "ship vX.Y.Z", "make a release", "tag and release", or asks to bump bacio's version. Also use it for pre-release dry runs that build artefacts locally to verify the matrix without publishing ("dry run", "snapshot build", "test the matrix").
---

# Release bacio

Releases are CI-driven: pushing a semver tag (`v0.1.0`, `v1.4.2`, optionally `-rc.1` etc.) triggers `.github/workflows/release.yml`, which runs `scripts/release.sh` on a clean Ubuntu runner, cross-compiles five archives, and publishes a GitHub release with the assets attached.

This skill handles the local side: pre-flight, version selection, confirmation, and `git tag` + `git push`. After the push, it watches the workflow and surfaces the release URL.

The build matrix lives in `scripts/release.sh` — single source of truth, callable both from CI and from a local dry-run.

## Scope

Run only the steps the user asked for. A "release" is steps 1, 2, 5, 6, 7 (+8 when it's a real user-facing release); a "dry run" is steps 1, 3, 4 and stops. Don't fold in adjacent tidying — no version-string edits, changelog rewrites, workflow tweaks, or dependency bumps unless the user asked. If a pre-flight fails, report it and stop; the fix is the user's call, not yours. Do this work in-session — there is nothing here worth a subagent.

## Workflow

### 1. Pre-flight checks

Fail fast on any error. Don't try to auto-fix dirty trees or rebases — those are user calls:

```bash
[ -f cmd/bacio/main.go ] || { echo "not in bacio repo"; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "working tree dirty; commit/stash first"; exit 1; }

branch="$(git rev-parse --abbrev-ref HEAD)"
[ "$branch" = "main" ] || echo "warning: releasing from $branch; confirm with the user"

git fetch --quiet origin
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/$branch)" ] || \
    echo "warning: local $branch differs from origin/$branch"

gh auth status >/dev/null 2>&1 || { echo "gh not logged in; run 'gh auth login'"; exit 1; }
gh workflow view release.yml >/dev/null 2>&1 || { echo "release.yml workflow missing — has it been pushed to origin?"; exit 1; }
```

### 2. Determine the version

Look up the most recent tag and propose the next semver:

```bash
last="$(git describe --tags --abbrev=0 2>/dev/null || echo none)"
echo "last tag: $last"
```

Suggest a bump based on what's changed since `$last`:

- **Patch** (`v0.1.0` → `v0.1.1`) — bug fixes only, no new behaviour.
- **Minor** (`v0.1.0` → `v0.2.0`) — new features, backwards-compatible.
- **Major** (`v0.1.0` → `v1.0.0`) — breaking changes (CLI surface, JSON shape, on-disk format).

For pre-1.0 projects, treat minor bumps as the place to land breaking changes too — that's the convention before the first stable release.

Confirm the chosen version with the user before tagging. The tag must match `vMAJOR.MINOR.PATCH` (with an optional `-prerelease` suffix); both `scripts/release.sh` and the workflow's tag filter (`v*`) enforce this.

### 3. (Optional) Local dry-run

If the user wants to verify the build matrix before tagging — or wants to inspect the artefacts a tester would download — run the script locally:

```bash
bash scripts/release.sh v0.1.0-dryrun
ls -lh dist/
```

This produces the same artefacts CI will produce, in `dist/`, without tagging or publishing. `dist/` is in `.gitignore`. Skip this step for routine releases — CI will catch any matrix issue.

### 4. Smoke-test the host binary (if you ran a dry-run)

Quick sanity check that the artefact for the current machine actually runs:

```bash
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
host="${host_os}-${host_arch}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ "$host_os" = "windows" ]; then
    unzip -q "dist/bacio-v"*"-${host}.zip" -d "$tmp"
else
    tar -xzf "dist/bacio-v"*"-${host}.tar.gz" -C "$tmp"
fi

"$tmp"/bacio-v*-${host}/bacio --help >/dev/null && echo "host binary OK"

# Also confirm the ldflag-injected version matches the tag you built
# (catches a broken -X path before tagging real assets).
"$tmp"/bacio-v*-${host}/bacio --version
```

### 5. Tag and push — the irreversible step. Confirm first.

This is the single irreversible action — pushing the tag triggers the release workflow on GitHub, which builds and publishes within a few minutes.

```bash
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

If anything is wrong with the tagged commit, you can `git tag -d v0.1.0 && git push origin :refs/tags/v0.1.0` to retract — but only if the workflow hasn't already published. Once the release is up, retraction means deleting the GitHub release too (`gh release delete v0.1.0`). Don't do this if the release was already announced or consumed.

### 6. Watch the workflow

The release workflow takes ~2–4 minutes on Ubuntu. Watch it from the terminal:

```bash
sleep 4   # give GitHub a moment to register the run
run_id="$(gh run list --workflow=release.yml --limit=1 --json databaseId --jq '.[0].databaseId')"
gh run watch "$run_id" --exit-status
```

`--exit-status` makes `gh run watch` return non-zero if the workflow fails. If it does fail:
- `gh run view "$run_id" --log` to see the logs
- Common causes: tests fail (fix and re-tag with a new patch version; don't move tags), build fails on a single platform (read the Go error), `gh release create` fails because a release already exists for that tag (delete it via `gh release delete vX.Y.Z` and re-trigger by re-pushing the tag).

### 7. Show the result

```bash
gh release view v0.1.0
```

Surface the release URL so the user can click through to verify the assets and changelog.

### 8. Sync the new docs to bacio.io

The user-docs site at [bacio.io](https://bacio.io) pins to a published release tag via `~/Repos/bacio-website/.bacio-ref`. Cutting a release doesn't deploy the new docs by itself — that's a separate, intentionally-pinned step on the website repo.

Hand off to the **`bacio-docs-update`** skill at `~/Repos/bacio-website/.claude/skills/bacio-docs-update/SKILL.md`. It bumps `.bacio-ref` to the latest published release (which is what step 5 just pushed), runs `npm run sync-docs`, commits to `main`, then merges `main → prod` for the Cloudflare Pages production deploy.

From this session, `cd ~/Repos/bacio-website` and follow the `bacio-docs-update` SKILL.md recipe — the skill auto-loads once the working directory changes, so a prompt like "sync the docs" or "bump the website to the latest release" picks it up.

Skip this step when:

- The release was a **pre-release** (`-rc.1`, `-beta`) — `gh release view` skips drafts/pre-releases by default, so the docs skill would no-op anyway, but it's clearer to not pretend you're publishing user-facing docs for an RC.
- The release contained **no user-doc changes** — `bacio-docs-update` would still bump `.bacio-ref` and resync (the footer's version chip would change), which is fine but adds a Cloudflare deploy for nothing. The skill's "bail early if pin == latest" check doesn't help here because the pin is whatever the *previous* release was. Use your judgement.

## Dry runs

When the user says "dry run", "snapshot", "build only", "test the matrix" — run **steps 1, 3, 4** only. Don't tag, don't push. This is the right mode for:

- Verifying the matrix after a dependency bump or Go upgrade.
- Producing local binaries for one-off testing or sharing with a single tester.
- Sanity-checking after editing `scripts/release.sh` or the workflow.

## Customising the matrix

The `PLATFORMS` array in `scripts/release.sh` is the single source of truth for which binaries get built — both locally and in CI. Edit it deliberately: release consumers expect a stable platform set across versions, and dropping one mid-stream breaks anyone who scripted a download URL.

If the user asks to add a Go-supported platform the matrix doesn't cover (e.g. `linux/arm` 32-bit, `freebsd/amd64`), it's a one-line addition. Discuss whether it's permanent or a one-off.

## Customising the workflow

`.github/workflows/release.yml` runs tests, then `scripts/release.sh`, then composes notes from `git log $previous..$tag`, then `gh release create`. If you want different release notes (e.g. driven by a CHANGELOG.md or GitHub's `--generate-notes`), edit the "Compose release notes" or "Create GitHub Release" steps. Keep tests in front of the build — a failing release is a much bigger problem than a 30-second test run.

## Troubleshooting

- **`gh auth status` fails locally** — run `gh auth login` and retry.
- **Workflow fails at `go test`** — broken `main`. Fix the test on `main`, then retag with the next patch version (`v0.1.1` if you tagged `v0.1.0`). Don't move tags after pushing.
- **Workflow fails at `scripts/release.sh`** — check the platform-specific error. Common causes: a CGO-using dep slipping in (bacio avoids these), or a missing build tag.
- **Workflow fails at `gh release create`** — usually because the release already exists. Either `gh release delete vX.Y.Z` and re-trigger by re-pushing the tag, or `gh release upload vX.Y.Z dist/...` to add the missing assets to the partial release.
- **Tagged the wrong commit** — `git tag -d vX.Y.Z && git push origin :refs/tags/vX.Y.Z`, then re-tag at the right HEAD and push. Only safe if the release isn't already announced/consumed.
- **Workflow doesn't trigger at all** — check that the tag matches the `v*` filter, that the tag was pushed (`git ls-remote --tags origin`), and that the workflow file is on `main` (workflows must exist on the default branch to be triggered by tag pushes).
