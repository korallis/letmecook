# Pinned ARM64 nftables setup helper

This helper is a short-lived deployment setup dependency. It has no credentials,
provider-state mount, account logic, HTTP client or retry policy. It only changes
the dedicated relay's network namespace, and must be removed before activation.

The manifest locks five Debian bookworm ARM64 package payloads. The signed
InRelease and by-hash Packages index are verified using the pinned base image's
Debian archive keyring. Dependency closure includes the observed base package
versions. `fetch-inputs.ts` retrieves only the locked public metadata/package
URLs; `verify-dependencies.ts` checks signatures, hashes and closure offline.
The large Packages index and .deb payloads are deliberately not checked in.

```sh
node experiments/native-evaluation/egress-helper/fetch-inputs.ts
cd experiments/native-evaluation/egress-helper
docker build --network=none --pull=false -t gaffer-native-egress-helper:bookworm-nft-1.0.6-locked .
```

Observed Linux ARM64 image ID:
`sha256:482b25318f295c63cfe19108c7c465039f7d98336c20b7f9db564fdd03af9cc0`.
The nft binary SHA256 is
`e39c5bf510d14d25dc8d685eb9f322f03481356fce341bc45870d9d331ba2a09`.
The verified binary is nftables v1.0.6. Added packages are not registered in dpkg;
use the verified manifest and binary hashes. Extraction preserves the base's
usrmerge symlinks and runs no package maintenance scripts/services.

The kernel proof is separate from image presence: run `../run-egress.ts` from the
repository root with the cached immutable helper. A different architecture or
image needs a separately measured and reviewed profile.
