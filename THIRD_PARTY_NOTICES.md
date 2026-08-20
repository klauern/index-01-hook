# Third-Party Notices

This file records dependency evidence that was verified in the local Go module
cache for the versions in `go.mod`. It does not claim that this inventory is
complete. The local evidence does not establish every license or component in
every build artifact.

This source file does not paste all license text. Release automation generates
`THIRD_PARTY_NOTICES.txt` with root license and notice files for the union of
Go modules linked by all release targets and the Go toolchain.

## Go modules

The following license families come from license files found for the exact
module versions in the local module cache.

| Module | Version | License evidence |
| --- | --- | --- |
| `modernc.org/sqlite` | `v1.56.0` | BSD-3-Clause wrapper license; `LICENSE`; bundled SQLite code public-domain dedication; `SQLITE-LICENSE` |
| `github.com/dustin/go-humanize` | `v1.0.1` | MIT; `LICENSE` |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause; `LICENSE` |
| `github.com/mattn/go-isatty` | `v0.0.24` | MIT; `LICENSE` |
| `github.com/ncruces/go-strftime` | `v1.0.0` | MIT; `LICENSE` |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | BSD-3-Clause; `LICENSE` |
| `golang.org/x/sys` | `v0.47.0` | BSD-3-Clause; `LICENSE` |
| `modernc.org/libc` | `v1.74.4` | BSD-3-Clause; `LICENSE`; additional `LICENSE-3RD-PARTY.md` |
| `modernc.org/mathutil` | `v1.7.1` | BSD-3-Clause; `LICENSE` |
| `modernc.org/memory` | `v1.11.0` | BSD-3-Clause; `LICENSE`; additional `LICENSE-GO`, `LICENSE-MMAP-GO`, and `LICENSE-LOGO` files |

The module cache also contains additional files for some modules. Release
automation must inspect the selected source, generated binary, and container
image instead of relying on this source-level table.

## Docker artifact contents

The `Dockerfile` copies these artifacts into the scratch image:

- The Certificate Authority (CA) certificate bundle supplied by `GO_IMAGE` at
  `/etc/ssl/certs/ca-certificates.crt`.
- Go time-zone data at `/usr/local/go/lib/time/zoneinfo.zip`.

Because `GO_IMAGE` selects the source image, the release notice process must
identify the source, version, and license terms for the exact CA bundle and
time-zone data included in each image. These are artifact-specific notice tasks.
This source file does not verify those terms.

Release binaries include the generated notice report as a checksummed, signed,
and attested asset. The container image embeds that report, the Go license, the
IANA time-zone notice, and the Debian Certificate Authority bundle copyright
file. The source SPDX SBOM and BuildKit SBOM provide complementary inventory
evidence.
