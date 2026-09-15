import { mkdirSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
// Called only for a new disposable fixture, never for an operator repository.
export function createFixture(directory: string) {
  mkdirSync(directory);
  const env = { PATH: process.env.PATH, HOME: directory, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_AUTHOR_DATE: '2026-09-14T00:00:00Z', GIT_COMMITTER_DATE: '2026-09-14T00:00:00Z' };
  const git = (...args: string[]) => execFileSync('git', ['-C', directory, ...args], { encoding: 'utf8', env });
  git('init', '-q', '--initial-branch=main'); writeFileSync(directory + '/greeting.txt', 'hello\n');
  git('add', 'greeting.txt'); git('-c', 'user.name=Harness Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'Public native fixture');
  return git('rev-parse', 'HEAD').trim();
}
