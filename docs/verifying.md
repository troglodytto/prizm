# Verifying a download

Every release carries a `checksums.txt`. Releases are **also** signed once the
maintainer's key is configured in CI — one signature covers the whole release,
since every archive's hash is in that file.

> **Not yet signed.** No release published so far carries `checksums.txt.asc`;
> the signing key is not yet in CI. Until it is, `checksums.txt` is what you
> can verify, and `PRIZM_REQUIRE_SIG=1` will correctly refuse to install.

`install.sh` checks both for you. This page is for doing it by hand, or for
understanding what the installer is doing on your behalf.

## By hand

```bash
tag=v0.6.2
base="https://github.com/troglodytto/prizm/releases/download/${tag}"

curl -fsSLO "${base}/prizm_${tag}_linux_amd64.tar.gz"
curl -fsSLO "${base}/checksums.txt"
curl -fsSLO "${base}/checksums.txt.asc"

gpg --recv-keys 49B66FF00161FF5AF6587CB59083374841288B9D
gpg --verify checksums.txt.asc checksums.txt
sha256sum -c checksums.txt --ignore-missing
```

`gpg --verify` must say **Good signature**. `sha256sum -c` must say **OK** for
the file you downloaded. If either complains, do not run the binary.

## What the installer does

By default it always verifies the checksum, and additionally verifies the
signature when `gpg` is installed and you already hold a release signing key.
If it cannot check the signature it says so and continues on the checksum
alone:

```
note: signature not checked — no release signing key is in your keyring
```

To make a signature mandatory — the right choice for anything automated:

```bash
PRIZM_REQUIRE_SIG=1 sh install.sh
```

That fails rather than installing if the release is unsigned, if `gpg` is
missing, or if the key is not in your keyring.

## Which key

The installer verifies against a list of known release fingerprints, not
against any key that happens to be in your keyring — a signature from an
arbitrary key proves only that somebody signed something.

| Fingerprint | Signed |
| --- | --- |
| `49B66FF00161FF5AF6587CB59083374841288B9D` | future signed releases |

Signing keys get rotated. A release stays signed by whichever key was current
when it was published, so old fingerprints stay on the list and older versions
keep verifying. The current list is `SIGNING_KEYS` at the top of
[`install.sh`](../install.sh).

## A caveat worth knowing

A checksum only proves the file you got matches the list. The signature is what
proves the list came from the maintainer — whoever can serve you a tarball can
serve you a matching hash just as easily. If you care about the difference, use
`PRIZM_REQUIRE_SIG=1`.
