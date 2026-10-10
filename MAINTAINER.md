# Maintaining Shelfmark

This guide covers CI, release publication, and bundled dependency updates.
See [BUILD.md](BUILD.md) for local builds and [README.md](README.md) for usage.

## GitHub builds and releases

GitHub Actions compiles and runs Go static analysis on pushes to `develop`/`main`
and pull requests. The **Build** workflow checks compilation and static analysis;
it does not publish downloadable artifacts. The **Release** workflow builds in
Docker and publishes Linux and Windows amd64 executables, their notices, a
downloadable Docker image, project and third-party sources, and SHA-256 checksums.
It does not push to a container registry.

Commit and push the source, then push a version tag to publish a release:

```sh
git tag -a v0.1.0 -m "Shelfmark 0.1.0"
git push origin v0.1.0
```

A tag such as `v0.1.0-rc.1` creates a prerelease. Follow the **Release** run under
**Actions**; downloads appear on the repository's **Releases** page. The release
title is the tag and its description uses GitHub's generated release notes, followed
by a download table linking directly to the Linux, Windows, and Docker artifacts.
The description can be edited after publication. Retrying a draft upload refreshes
the download table while preserving the rest of the description.

Manual runs of the **Release** workflow require an existing unpublished tag and
leave a draft for review. The workflow must be present on the repository's default
branch for [manual runs](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow).
Pushing a new version tag triggers automatic publication regardless of manual runs.

Automatic tag runs publish only after all assets and matching sources have
uploaded. Published versions are not overwritten; use a new tag. An interrupted
upload can leave a draft, which can be retried. The workflow uses the repository's `GITHUB_TOKEN`
with release write permission; no additional secrets are required. GitHub Actions
must be enabled and repository policies must allow the job's write permission.

The published filenames and their purposes are listed in the
[download guide](README.md#downloads).

Large third-party source archives are split into numbered parts. Concatenate them
in filename order, then extract the reconstructed archive. `SHA256SUMS` covers
all assets, including individual parts. Keep matching sources and notices with
the release when redistributing binaries; see [Third-party software](THIRD_PARTY.md).

## Release preparation

Before tagging a version:

1. Commit and push the intended source, including documentation and dependency
   manifests. The release workflow builds the source referenced by the tag.
2. Check the **Build** workflow result for that commit.
3. Confirm that the user guide describes the release's supported platforms and
   runtime requirements.
4. Choose a new version tag. Use a prerelease suffix such as `-rc.1` for a release
   candidate; pushing the tag starts the release workflow automatically.

After the workflow publishes the release, review the assets and edit the generated
release description with highlights, known limitations, and any upgrade notes.

## Bundled dependencies

Native dependency versions, download URLs, and checksums are recorded in:

- [OCR sources and models](runtime/ocr/sources.json).
- [OCR compiler-runtime notices](runtime/ocr/licenses.json).
- [Windows WebView2 runtime and loader](desktop/webview/windows/runtime.json).
- [WebView2 runtime terms](licenses/webview2-terms.json).
- [Embedded font attribution](licenses/fonts.json).

When updating a dependency, update its matching notices and source information as
well. Rebuild the affected targets; a bundled runtime update reaches users through
a new Shelfmark executable. The Fixed Version WebView2 runtime does not update
itself on the destination machine.

The Go toolchain is specified in [go.mod](go.mod) and the container build toolchain
in [Dockerfile](Dockerfile). Keep them compatible when updating either one.

Release source collection must resolve the exact dependency versions used in the
build. Missing sources stop publication rather than substituting a different
version. Preserve the source and notice downloads alongside published binaries;
see [THIRD_PARTY.md](THIRD_PARTY.md) for redistribution details.
