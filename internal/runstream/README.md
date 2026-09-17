# Provisional run output ordering and spooling (#17)

This reversible **local library** advances
[#17](https://github.com/korallis/letmecook/issues/17). It does not close that
issue or qualify an execution runtime. No process produces these records, no
transport carries them and no harness adapter is integrated. `internal/runner`
still refuses launch with `runner_execution_unqualified`. #9/#10 reconciliation,
#16 supervisor evidence and #6 real adapter acceptance remain outstanding.

## Framing

`Version` is `stream-provisional-v1`, deliberately separate from the closed
`execution-provisional-v2` message union in `schemas/execution`. The contract
lists the stream encoding as an open O2 obligation, so new kinds are **not**
added to the deployed union; this framing is local durability input that a
selected transport must still reconcile, not a wire specification.

Each `Record` carries the full execution `Identity`, a 1-based `Sequence`, the
native byte `Offset` of its chunk, the exact `Native` harness event
(`{version, kind, data}`) and the bounded UTF-8 `Normalized` projection
(`stdout`, `stderr` or `status`). Normalization never replaces native bytes, so
a later consumer is not bound to this slice's projection choices. `Digest`
covers the canonical record and proves self-consistency only; it authenticates
nothing and every boundary revalidates with `Validate`.

## Ordering, acknowledgement and reconnect

`Spool` is the sender side. `Append` assigns the next sequence and offset and
returns only after the record is synced. `Acknowledge` persists a receiver
watermark that is monotonic and idempotent: stale and repeated watermarks are
no-ops, and a watermark beyond what was appended is refused, so a lost or forged
acknowledgement can never imply undurable work is safe. `Pending` is the exact
resend set after a reconnect; `Stats` keeps locally appended work separate from
remotely acknowledged work.

`Sink` is the receiver side. `Receive` accepts only `Expected`. An earlier
sequence replays the retained acknowledgement without creating a second logical
record, a changed payload for a retained sequence returns
`runstream_record_conflict`, and a later sequence returns
`runstream_sequence_gap` with the expected sequence rather than buffering
out-of-order input. Non-contiguous byte offsets and foreign identities refuse.
A positive `Ack` follows the durable write; it never precedes it.

Reopening either side replays the log and refuses any history that does not
match this version, role and identity. A changed generation is reconciliation
input, never a resume.

## Storage and bounds

Both sides persist through `internal/runnerjournal`: an append-only SHA-256
chained log in a mode-0700 owned directory with an exclusive lock, file and
directory sync before acknowledgement, and a poisoned handle on any uncertain
write. Chunk payloads are capped at 48 KiB native bytes and 8 KiB normalized
text; the spool limit is 64 KiB to 4 MiB and is chosen at creation.

A full spool returns `runstream_spool_full`, refusing new output while keeping
every retained record recoverable. There is no truncation, compaction or
oldest-record eviction: exhaustion is an explicit backpressure signal for the
producer, and only acknowledgement progress may still be recorded, from a small
reserve above the chunk budget. Memory is bounded by the same limit because the
journal retains its rows.

Run:

```sh
go test -race -count=1 ./internal/runstream
CGO_ENABLED=0 go test -count=1 ./internal/runstream
go vet ./internal/runstream
```

Tests cover reconnect resume, duplicate and out-of-order delivery, tampered and
foreign-identity records, ownership loss, spool exhaustion with continued
acknowledgement, and a real SIGKILLed receiver whose durable watermark survives
and whose lost acknowledgement is deduplicated on replay.

Still missing: the selected transport and its encodings, runner and daemon
integration, browser presentation, retention and compaction policy, artifact
custody (#18) and the measured runtime evidence behind O2/O4/O6. Passing these
tests establishes library behavior only, not supported execution or original M1
acceptance.
