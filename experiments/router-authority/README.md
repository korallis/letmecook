# Router authority source experiment

This experiment executes four hash-pinned, unmodified public modules from 9Router
0.5.75 with synthetic imports and time. It demonstrates why the existing dashboard
counter and combo writer cannot implement the accepted live `FrozenAuthority`.
It never starts a router, installs its dependencies or contacts a provider.

Read the [observations and integration requirements](../../docs/evidence/router-authority.md).

## Reproduce

Use a disposable checkout of public `decolua/9router` at
`17c4cc76877bd1755030a8414f8d0083f48dcccf`. Do not use a live installation, private
configuration export or credentials. Source files are data until executed inside
the container; do not run the upstream installer, scripts or server on the host.
The probe verifies all four SHA-256 digests before evaluating any upstream module.

Select your Docker context and absolute paths explicitly. The example variables
are local choices, never product defaults. Pull the pinned image before the run if
it is absent. The container itself has no network.

```sh
probe_context=YOUR_DOCKER_CONTEXT
probe_source=/ABSOLUTE/PATH/TO/PUBLIC/9ROUTER/CHECKOUT
probe_code=/ABSOLUTE/PATH/TO/GAFFER/experiments/router-authority
probe_image=node:24-bookworm@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0
docker --context "$probe_context" pull "$probe_image"
docker --context "$probe_context" run --rm \
  --label dev.gaffer.authority-evidence=manual \
  --network none --user 1000:1000 --cap-drop ALL \
  --security-opt no-new-privileges=true --read-only --init \
  --cgroupns private --ipc private --cpus 0.5 \
  --memory 128m --memory-swap 128m --pids-limit 64 \
  --ulimit nofile=256:256 --ulimit core=0:0 \
  --mount "type=bind,source=$probe_code,target=/probe,readonly" \
  --mount "type=bind,source=$probe_source,target=/router-source,readonly" \
  --env HOME=/nonexistent --env NODE_OPTIONS= --pull never "$probe_image" \
  timeout --signal=TERM --kill-after=1s 15s \
  node --experimental-vm-modules /probe/probe.ts /router-source
docker --context "$probe_context" ps -aq \
  --filter label=dev.gaffer.authority-evidence=manual
```

Success emits one JSON record and the final container query is empty. An expected
Node VM experimental warning goes to stderr. A digest or behavioural mismatch
fails with a nonzero exit; do not update the pin merely to make it pass. The
`/.dockerenv` check prevents accidental ordinary host invocation, but does not
attest isolation. The documented Docker options are the isolation mechanism;
`node:vm` is only a way to replace imports and time, not a security boundary.
This is not a revalidation of the worker isolation profile from #5.

Typecheck the Gaffer-owned experiment with Node 24+:

```sh
npm ci --prefix experiments/router-authority --ignore-scripts
npm --prefix experiments/router-authority run check
```

The workflow runs the same proof against a separate public checkout at the pinned
commit. No upstream source is vendored. The four modules' dependent framework,
database and timer functions are intentionally replaced by minimal adapters; the
test does not exercise actual HTTP authentication, SQLite or provider work.
