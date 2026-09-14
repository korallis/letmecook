#!/usr/bin/env node
// Launcher lifecycle double only; this is never mounted in real proof containers.
import { appendFileSync, existsSync, readFileSync, readdirSync, unlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const dir = process.env.FAKE_DOCKER_STATE!;
const profile = JSON.parse(process.env.FAKE_DOCKER_PROFILE!);
const input = process.argv.slice(2);
if (input[0] !== '--context' || input[1] !== 'test-only') throw new Error('Unexpected Docker context');
const args = input.slice(2);
const cmd = args[0];
const label = 'dev.gaffer.isolation-experiment';
const log = (value: unknown) => appendFileSync(join(dir, 'calls.jsonl'), JSON.stringify(value) + '\n');
const out = (value: unknown) => process.stdout.write(typeof value === 'string' ? value : JSON.stringify(value));
const file = (id: string) => join(dir, id + '.json');
const read = (id: string) => JSON.parse(readFileSync(file(id), 'utf8'));
const save = (id: string, value: unknown) => writeFileSync(file(id), JSON.stringify(value));
const val = (key: string) => args[args.indexOf(key) + 1];
async function interrupt(stage: string) {
  if (process.env.FAKE_DOCKER_STOP !== stage || existsSync(join(dir, 'signalled'))) return;
  writeFileSync(join(dir, 'signalled'), 'yes');
  log(['signal', stage, Date.now()]);
  process.kill(process.ppid, process.env.FAKE_DOCKER_SIGNAL as NodeJS.Signals);
  // Keep the CLI pending while the launcher handles the signal. A wait/logs call
  // must be cancelled rather than delaying cleanup until its normal timeout.
  await delay(stage === 'wait' || stage === 'event' ? 30_000 : 200);
}
log(args);
if (cmd === 'version') out({ Client: { Version: profile.engineVersion }, Server: {
  Version: profile.engineVersion, Os: 'linux', Arch: profile.architecture, KernelVersion: profile.kernelVersion,
} });
else if (cmd === 'info') out({ CgroupVersion: '2', MemoryLimit: true, SwapLimit: true,
  CpuCfsQuota: true, PidsLimit: true, SecurityOptions: ['name=seccomp,profile=builtin'] });
else if (cmd === 'image') out([{}]);
else if (cmd === 'volume') {
  if (args[1] === 'create') {
    save(args.at(-1)!, { Labels: { [label]: val('--label').split('=')[1] } });
    out(args.at(-1));
  } else if (args[1] === 'inspect') out([read(args[2])]);
  else if (args[1] === 'rm') unlinkSync(file(args[2]));
  else if (args[1] === 'ls') out(readdirSync(dir).filter(f => f.endsWith('-socket.json')).join('\n'));
  else throw new Error('Unexpected volume operation');
} else if (cmd === 'create') {
  const name = val('--name');
  const mounts = args.filter((_, i) => args[i - 1] === '--mount');
  const parse = (mount: string) => Object.fromEntries(mount.split(',').map(s => s.split('=')));
  const bind = parse(mounts[0]), volume = parse(mounts[1]);
  save(name, {
    Config: { Image: profile.image, User: val('--user'), Labels: {
      [label]: process.env.FAKE_DOCKER_WRONG_OWNER && name.endsWith('-probe') ? 'another-run' : val('--label').split('=')[1],
    } },
    HostConfig: { NetworkMode: val('--network'), Privileged: false, ReadonlyRootfs: true,
      CapDrop: ['ALL'], SecurityOpt: ['no-new-privileges=true'], Init: true, PidMode: '',
      IpcMode: 'private', CgroupnsMode: 'private', Memory: 134217728, MemorySwap: 134217728,
      NanoCpus: 500000000, PidsLimit: 64, ShmSize: 4194304, RestartPolicy: { Name: 'no' },
      LogConfig: { Type: 'local', Config: { 'max-file': '1', 'max-size': '1m', compress: 'false' } },
      Tmpfs: Object.fromEntries(args.filter((_, i) => args[i - 1] === '--tmpfs').map(s => {
        const colon = s.indexOf(':'); return [s.slice(0, colon), s.slice(colon + 1)];
      })),
    },
    Mounts: [{ Type: 'bind', Source: bind.source, Destination: '/fixture', RW: false },
      { Type: 'volume', Name: volume.source, Destination: '/router', RW: !mounts[1].endsWith(',readonly') }],
    State: { Running: false },
  });
  if (name.endsWith('-probe')) await interrupt('create');
  out(name);
} else if (cmd === 'inspect') {
  if (args[1].endsWith('-probe')) await interrupt('inspect');
  out([read(args[1])]);
} else if (cmd === 'start') {
  const state = read(args[1]); state.State.Running = true; save(args[1], state); out(args[1]);
} else if (cmd === 'logs') {
  if (args[1].endsWith('-gateway')) await interrupt('event');
  out(args[1].endsWith('-gateway') ? '{"ready":true}' : '{"name":"probe-complete"}');
} else if (cmd === 'wait') { await interrupt('wait'); out('0'); }
else if (cmd === 'rm') { log(['remove', args.at(-1), Date.now()]); unlinkSync(file(args.at(-1)!)); }
else if (cmd === 'ps') out(readdirSync(dir).filter(f => /^gaffer-isolation-.*(?<!-socket)\.json$/.test(f)).join('\n'));
else throw new Error('Unexpected Docker command: ' + JSON.stringify(args));
