# Releasing

Tagging is the whole process. Pushing a `v*` tag runs
[`.github/workflows/release.yml`](../.github/workflows/release.yml), which
builds every platform, signs the checksums, and creates the GitHub release
using the tag's own annotation as the notes.

```bash
git tag -s v0.6.0 -F -   # annotated and signed; the message becomes the notes
git push origin main
git push origin v0.6.0
```

To rebuild a tag that already exists — a failed runner, a workflow fix — use
the `workflow_dispatch` trigger with the tag name rather than moving the tag.

## What gets built

| Platform | Runner | Linking |
| --- | --- | --- |
| linux/amd64 | `ubuntu-latest` | static (musl) |
| linux/arm64 | `ubuntu-24.04-arm` | static (musl) |
| darwin/arm64 | `macos-14` | dynamic against libSystem |
| darwin/amd64 | `macos-13` | dynamic against libSystem |

Storage is SQLite through cgo, so none of this cross-compiles from one
runner — each OS builds on its own machine.

Linux links statically against musl because a glibc-linked binary carries the
glibc floor of whatever produced it. A stock `ubuntu-latest` build wants
GLIBC_2.34, which silently excludes Ubuntu 20.04, Debian 11 and RHEL 8. Three
build tags make the static link clean:

- `osusergo` — pure-Go `os/user`, which otherwise wants `getgrouplist`
- `netgo` — pure-Go DNS
- `sqlite_omit_load_extension` — drops the `dlopen` SQLite would need

Without all three the link succeeds but warns that it still requires shared
libc at runtime, which defeats the point.

Windows is absent: the apply lock uses `flock`. Porting it means a
`LockFileEx` implementation behind a build tag, plus a Windows runner in the
matrix, plus mingw for cgo SQLite.

## Signing

The release job signs `checksums.txt` when `GPG_PRIVATE_KEY` is set, and
verifies its own signature before publishing. Without the secret the release
still goes out, unsigned — a missing key should not block a release, but a
broken signature must.

Set it up once:

```bash
# Export the private key. Treat this output as the secret it is.
gpg --armor --export-secret-keys 49B66FF00161FF5AF6587CB59083374841288B9D

gh secret set GPG_PRIVATE_KEY   # paste the block, including BEGIN/END lines
gh secret set GPG_PASSPHRASE    # omit if the key has no passphrase
```

The fingerprint is pinned in [`install.sh`](../install.sh) as `SIGNING_KEY`.
If the signing key ever changes, that constant changes with it — the installer
verifies against that fingerprint specifically, not against any key the user
happens to hold.

**The key expires.** A signature made before expiry still verifies afterwards,
but `gpg --recv-keys` will hand new users an expired key and they will
reasonably not trust it. Extend it before it lapses:

```bash
gpg --quick-set-expire 49B66FF00161FF5AF6587CB59083374841288B9D 1y
gpg --send-keys 49B66FF00161FF5AF6587CB59083374841288B9D
```

## Checking a release afterwards

```bash
PRIZM_REQUIRE_SIG=1 PRIZM_VERSION=v0.6.0 sh install.sh
```

That exercises the real download, the signature and the checksum in one go. It
is worth running once per release from a machine that is not the one that cut
it.
