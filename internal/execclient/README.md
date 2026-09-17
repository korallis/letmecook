# Execution client

The client uses TLS 1.3, an explicit runner certificate, the pinned daemon leaf
SHA-256 and exactly ALPN `execution-provisional-v2`. Its transport is HTTP/1.1
only, with no proxy discovery, redirects or HTTP/2 upgrade. Every call after
hello sends exactly one `X-Gaffer-Session`.

Methods cover session, state, inbox, task input, messages, leases, ordered stream,
begin/blob/commit, usage and finalize. Closed bounded JSON is checked on both
sides of the client boundary. The caller allocates and journals message IDs;
transport retries reuse the exact serialized body and key. Blobs use their
content digest as the key and preserve explicit zero-byte Content-Length.
Generation/boot/session refusals are distinguishable via `IsFence`.

`TaskInput` temporarily mirrors S1's additional input type. `Digest` canonicalizes
nested settings with preserved numeric tokens and sorted object keys. `Validate`
binds that digest and the full task identity/paths/operations to the dispatch;
typed data alone is not launch authority. The supervisor still calls `Accept`
with independently loaded local policy before launching.

The current S1 session body may omit its daemon fingerprint (empty string).
That does not weaken TLS pinning; a nonempty conflicting body field refuses.
Session timings are validated, not hardcoded (S1 currently advertises 2s drift,
5s termination, 20s validity and 5s renewal). Revoked/disabled credentials or
boots are non-retryable fencing outcomes, not transient network failures.
