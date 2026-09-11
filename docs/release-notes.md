Download `ghrctl-linux-amd64`, `checksums.txt`, and `release-manifest.json`.
Verify with `sha256sum --check checksums.txt`, install the executable as
`ghrctl`, then run `ghrctl start` on a Linux x64 Docker/Compose v2 host.

The manifest records the matching Docker Hub image digest. Existing
installations retain their selected image until `ghrctl upgrade` is run.

This draft must remain unpublished until a maintainer records live acceptance
using these exact assets according to `docs/binary.md`. Replace this paragraph
with the host/platform, version, image digest, date, and acceptance results
before publishing. Do not include credentials in release notes.
