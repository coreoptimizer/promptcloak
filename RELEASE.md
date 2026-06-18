# Releasing promptcloak

Releases are cut by pushing a SemVer tag. The [`release`](.github/workflows/release.yml)
workflow then publishes, all to **ghcr.io**:

- the multi-arch container image `ghcr.io/coreoptimizer/promptcloak/extproc`
  (`linux/amd64,linux/arm64`), tagged `X.Y.Z`, `X.Y`, and `latest`;
- the Helm chart as an OCI artifact at
  `oci://ghcr.io/coreoptimizer/promptcloak/charts/promptcloak`, version `X.Y.Z`;
- a GitHub Release with auto-generated notes and the packaged chart `.tgz`.

## Cut a release

1. Make sure `main` is green (the `ci` workflow runs on every push/PR) and that
   the working tree is clean.
2. Tag and push:

   ```sh
   git tag v0.1.0
   git push origin v0.1.0
   ```

   The tag (minus the leading `v`) becomes the image tag, the chart `version`,
   and the chart `appVersion`. `Chart.yaml`'s committed values are placeholders;
   the workflow overrides them with `--version` / `--app-version` at package time.
3. Watch the run under the repo's **Actions** tab.

## One-time setup

GitHub Actions authenticates to ghcr.io with the built-in `GITHUB_TOKEN`
(`packages: write` is granted in the workflow), so no extra secret is needed.

However, packages created by Actions are **private by default**. For anonymous
`docker pull` / `helm install` to work, set each package's visibility to public
once it exists (after the first release):

- GitHub → your org/user → **Packages** → `extproc` → *Package settings* →
  *Change visibility* → **Public**.
- Repeat for the `charts/promptcloak` package.

(Alternatively keep them private and have consumers `helm registry login` /
`docker login` to ghcr.io with a token that has `read:packages`.)

## Verify a published release

```sh
docker pull ghcr.io/coreoptimizer/promptcloak/extproc:0.1.0
helm pull oci://ghcr.io/coreoptimizer/promptcloak/charts/promptcloak --version 0.1.0
```

## Pre-releases

Use a pre-release tag (e.g. `v0.1.0-rc.1`) to validate the published artifacts
before cutting the final tag. SemVer pre-release suffixes flow through to both
the image and chart versions.
