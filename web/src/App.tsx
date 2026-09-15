import * as stylex from '@stylexjs/stylex';
import { useCallback, useEffect, useState } from 'react';
import type { Event, Snapshot, Task } from '../../schemas/readapi/types.ts';
import { readSnapshot, STATUS_WORDS, type ReadState } from './read.ts';
import { colors, space, type } from './tokens.stylex.ts';

const NARROW = '@media (max-width: 40rem)';
const REDUCED = '@media (prefers-reduced-motion: reduce)';

const s = stylex.create({
  page: {
    backgroundColor: colors.surface,
    color: colors.ink,
    fontFamily: type.bodyFamily,
    fontSize: type.size,
    lineHeight: type.lineHeight,
    margin: 0,
    minHeight: '100vh',
  },
  main: { maxWidth: space.measure, marginInline: 'auto', paddingBlock: space.lg, paddingInline: { default: space.lg, [NARROW]: space.md } },
  header: { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: space.md, borderBottomWidth: 2, borderBottomStyle: 'solid', borderBottomColor: colors.ink, paddingBottom: space.sm, marginBottom: space.lg },
  title: { fontFamily: type.family, fontSize: type.sizeTitle, fontWeight: 700, letterSpacing: type.tracking, textTransform: 'uppercase', margin: 0 },
  label: { fontFamily: type.family, fontSize: type.sizeSmall, letterSpacing: type.tracking, textTransform: 'uppercase', color: colors.inkMuted },
  status: { fontFamily: type.family, fontWeight: 700, marginBlock: space.md },
  button: {
    fontFamily: type.family,
    fontSize: type.sizeSmall,
    letterSpacing: type.tracking,
    textTransform: 'uppercase',
    color: colors.ink,
    backgroundColor: colors.surfaceRaised,
    borderWidth: 1,
    borderStyle: 'solid',
    borderColor: colors.ink,
    paddingBlock: space.sm,
    paddingInline: space.md,
    cursor: { default: 'pointer', ':disabled': 'wait' },
    outlineWidth: { default: 0, ':focus-visible': 3 },
    outlineStyle: 'solid',
    outlineColor: colors.focus,
    outlineOffset: 2,
    transitionProperty: { default: 'background-color', [REDUCED]: 'none' },
    transitionDuration: '120ms',
  },
  section: { marginBlock: space.lg },
  heading: { fontFamily: type.family, fontSize: type.size, letterSpacing: type.tracking, textTransform: 'uppercase', marginBlock: space.sm, borderBottomWidth: 1, borderBottomStyle: 'solid', borderBottomColor: colors.rule, paddingBottom: space.xs },
  meta: { display: 'grid', gridTemplateColumns: { default: 'max-content 1fr', [NARROW]: '1fr' }, columnGap: space.lg, rowGap: space.xs, margin: 0 },
  dt: { fontFamily: type.family, fontSize: type.sizeSmall, letterSpacing: type.tracking, textTransform: 'uppercase', color: colors.inkMuted },
  dd: { margin: 0, fontFamily: type.family, overflowWrap: 'anywhere', marginBottom: { default: 0, [NARROW]: space.sm } },
  scroll: { overflowX: 'auto', outlineWidth: { default: 0, ':focus-visible': 3 }, outlineStyle: 'solid', outlineColor: colors.focus, outlineOffset: 2 },
  table: { borderCollapse: 'collapse', width: '100%', fontFamily: type.family, fontSize: type.sizeSmall },
  th: { textAlign: 'left', letterSpacing: type.tracking, textTransform: 'uppercase', color: colors.inkMuted, fontWeight: 400, paddingBlock: space.xs, paddingInline: space.sm, borderBottomWidth: 1, borderBottomStyle: 'solid', borderBottomColor: colors.rule, whiteSpace: 'nowrap' },
  td: { paddingBlock: space.xs, paddingInline: space.sm, borderBottomWidth: 1, borderBottomStyle: 'solid', borderBottomColor: colors.rule, verticalAlign: 'top', whiteSpace: 'nowrap' },
  note: { color: colors.inkMuted, fontSize: type.sizeSmall, marginBlock: space.md },
  list: { margin: 0, paddingInlineStart: space.lg, fontFamily: type.family },
});

function Meta({ snapshot }: { snapshot: Snapshot }) {
  return (
    <section {...stylex.props(s.section)} aria-labelledby="meta-heading">
      <h2 id="meta-heading" {...stylex.props(s.heading)}>Daemon metadata</h2>
      <dl {...stylex.props(s.meta)}>
        <dt {...stylex.props(s.dt)}>Mode</dt><dd {...stylex.props(s.dd)}>{snapshot.mode}</dd>
        <dt {...stylex.props(s.dt)}>Read version</dt><dd {...stylex.props(s.dd)}>{snapshot.version}</dd>
        <dt {...stylex.props(s.dt)}>Schema version</dt><dd {...stylex.props(s.dd)}>{snapshot.schema_version}</dd>
        <dt {...stylex.props(s.dt)}>Generation</dt><dd {...stylex.props(s.dd)}>{snapshot.generation}</dd>
        <dt {...stylex.props(s.dt)}>Daemon boot</dt><dd {...stylex.props(s.dd)}>{snapshot.daemon_boot}</dd>
        <dt {...stylex.props(s.dt)}>Missing capabilities</dt>
        <dd {...stylex.props(s.dd)}>
          <ul {...stylex.props(s.list)}>{snapshot.missing_capabilities.map(c => <li key={c}>{c}</li>)}</ul>
        </dd>
      </dl>
    </section>
  );
}

function Tasks({ tasks }: { tasks: Task[] }) {
  return (
    <section {...stylex.props(s.section)} aria-labelledby="tasks-heading">
      <h2 id="tasks-heading" {...stylex.props(s.heading)}>Tasks ({tasks.length}, bounded)</h2>
      <div {...stylex.props(s.scroll)} tabIndex={0} role="group" aria-label="Tasks table, scroll horizontally">
        <table {...stylex.props(s.table)}>
          <thead><tr>
            <th scope="col" {...stylex.props(s.th)}>Task</th>
            <th scope="col" {...stylex.props(s.th)}>Task state</th>
            <th scope="col" {...stylex.props(s.th)}>Attempt state</th>
            <th scope="col" {...stylex.props(s.th)}>Epoch</th>
            <th scope="col" {...stylex.props(s.th)}>Revision</th>
            <th scope="col" {...stylex.props(s.th)}>Desired</th>
            <th scope="col" {...stylex.props(s.th)}>Process</th>
            <th scope="col" {...stylex.props(s.th)}>Remote work</th>
            <th scope="col" {...stylex.props(s.th)}>Quarantined</th>
          </tr></thead>
          <tbody>
            {tasks.map(t => (
              <tr key={t.task_id}>
                <td {...stylex.props(s.td)}>{t.task_id}</td>
                <td {...stylex.props(s.td)}>{t.state}</td>
                <td {...stylex.props(s.td)}>{t.attempt.state}</td>
                <td {...stylex.props(s.td)}>{t.attempt.identity.epoch}</td>
                <td {...stylex.props(s.td)}>{t.attempt.revision}</td>
                <td {...stylex.props(s.td)}>{t.attempt.observation.desired}</td>
                <td {...stylex.props(s.td)}>{t.attempt.observation.confirmed_process}</td>
                <td {...stylex.props(s.td)}>{t.attempt.observation.remote_work}</td>
                <td {...stylex.props(s.td)}>{t.attempt.observation.quarantined ? 'yes' : 'no'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Events({ events }: { events: Event[] }) {
  return (
    <section {...stylex.props(s.section)} aria-labelledby="events-heading">
      <h2 id="events-heading" {...stylex.props(s.heading)}>Events ({events.length}, bounded; not a complete history)</h2>
      <div {...stylex.props(s.scroll)} tabIndex={0} role="group" aria-label="Events table, scroll horizontally">
        <table {...stylex.props(s.table)}>
          <thead><tr>
            <th scope="col" {...stylex.props(s.th)}>Sequence</th>
            <th scope="col" {...stylex.props(s.th)}>Revision</th>
            <th scope="col" {...stylex.props(s.th)}>Kind</th>
            <th scope="col" {...stylex.props(s.th)}>Detail</th>
            <th scope="col" {...stylex.props(s.th)}>Message</th>
          </tr></thead>
          <tbody>
            {events.map(e => (
              <tr key={e.sequence}>
                <td {...stylex.props(s.td)}>{e.sequence}</td>
                <td {...stylex.props(s.td)}>{e.revision}</td>
                <td {...stylex.props(s.td)}>{e.message.kind}</td>
                <td {...stylex.props(s.td)}>
                  {e.message.kind === 'assign' ? `route ${e.message.route.route_ref}` : `${e.message.from} to ${e.message.to} (expected revision ${e.message.expected_revision})`}
                </td>
                <td {...stylex.props(s.td)}>{e.message.message_id}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

export function App() {
  const [state, setState] = useState<ReadState>({ kind: 'loading' });
  const refresh = useCallback(() => {
    setState({ kind: 'loading' });
    void readSnapshot().then(setState);
  }, []);
  useEffect(refresh, [refresh]);
  const snapshot = state.kind === 'ready' || state.kind === 'empty' ? state.snapshot : null;
  const detail = state.kind === 'server-error' ? ` (HTTP ${state.status}, code ${state.code})` : state.kind === 'malformed' ? ` (HTTP ${state.status})` : '';
  return (
    <div {...stylex.props(s.page)}>
      <main {...stylex.props(s.main)}>
        <header {...stylex.props(s.header)}>
          <h1 {...stylex.props(s.title)}>Gaffer</h1>
          <span {...stylex.props(s.label)}>Fixture-only read view</span>
          <span {...stylex.props(s.label)}>Provisional: no execution, acceptance or sessions</span>
        </header>
        <p role="status" aria-live="polite" {...stylex.props(s.status)}>{STATUS_WORDS[state.kind]}{detail}</p>
        <button type="button" {...stylex.props(s.button)} onClick={refresh} disabled={state.kind === 'loading'}>Refresh snapshot</button>
        {snapshot && <Meta snapshot={snapshot} />}
        {snapshot && <Tasks tasks={snapshot.tasks} />}
        {snapshot && <Events events={snapshot.events} />}
        <p {...stylex.props(s.note)}>
          Public disposable fixture read over loopback. Nothing here starts, halts, accepts, acknowledges, publishes or merges work; authentication, sessions, task actions and router configuration do not exist in this slice.
        </p>
      </main>
    </div>
  );
}
