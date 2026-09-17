# Synthetic subprocess harness

`fake.New(binary)` runs `gaffer-runner fake-job --spec <private-file>` through the
supplied launcher. There is no in-process success mock and no gateway inference.
The job gets a clean HOME/XDG/TMP environment, not ambient credentials/config.

The task-input settings schema is the corpus's direct object:

```json
{"attempts":[{"mode":"edit","edits":[{"path":"greeting.txt","content":"hello, gaffer\n"}]}]}
```

Epoch N uses `attempts[min(N-1,last)]`. Unit tests may pass one Settings object.
Modes: `edit`, `noop`, `delete`, `create_empty`, `binary_edit`, `hang`, `crash`,
`approval`, `ignore_term`, `huge_output`, `exit_nonzero`, `fork_child`,
`detached_child`, `crash_after_edit`. Edits have `path`, optional `content`,
`content_base64` or `delete:true`; optional `delay_ms` and `stream_bytes` are
bounded. Unknown/null fields, traversal and `.git` edits refuse. Hang/crash/
approval do not apply declared edits; crash_after_edit does.

Approval emits an event then waits, never grants itself permission. Ignore-term
and forked children are real signal targets. Detached-child calls setsid; the
guardian must report unknown containment, not a release. Huge output flows
through real pipes and durable spool limits. Native JSON lines are retained with
normalized status/activity, and nonzero subprocess exit emits a failed event.

Readers retain a bounded 8 MiB event window per run without waiting on the event
consumer, so a full supervisor spool cannot strand scanner goroutines. Hitting
the window closes the read pipes, retains an explicit error and refuses release.
At most eight unresolved runs are retained. `Release(ctx, handle, through)` prunes
only completed, fully consumed runs at the exact final event watermark after the
supervisor's durable custody gate; incomplete output is never dropped to release.
