import { fileURLToPath } from 'node:url';
export const FIXTURE_HASH = 'cfbd4f521e876bf7c58582c01f8e6d79b81f8203ba2d98f8fd2258853bffe4e5';
export const FIXTURE_ROOT = fileURLToPath(new URL('../../tests/fixtures/planner', import.meta.url));
export const BRIEF = 'Propose correcting the greeting spelling in fixture.txt. Preserve other text. Do not implement or publish the change.';
export function proposal(revision: string, fileRead = true) {
  const refs = fileRead ? ['capture', 'fixture.txt'] : ['capture'];
  return { schema: 1, kind: 'plan_proposal', input_revision: revision, outcome: 'Correct the greeting spelling.', constraints: ['Preserve other text.'], exclusions: ['Execution and delivery require separate authority.'],
    criteria: [{ id: 'c1', statement: 'The greeting spells world correctly.', evidence_required: 'Review the proposed one-word diff.' }], allowed_paths: ['fixture.txt'], allowed_systems: [], assumptions: [], unresolved_questions: [],
    steps: [{ id: 's1', description: 'Propose replacing wrld with world in the greeting.', criterion_ids: ['c1'], source_refs: refs }],
    assessment: { work_class: 'text_edit', consequence: 'low', unknowns: [], source_refs: refs } };
}
