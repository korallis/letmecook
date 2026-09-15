import * as stylex from '@stylexjs/stylex';

// Single token file for the shell. Light/dark pairs keep text contrast >= 4.5:1
// against their surfaces; status is conveyed by words, never by colour alone.
const DARK = '@media (prefers-color-scheme: dark)';

export const colors = stylex.defineVars({
  surface: { default: '#ffffff', [DARK]: '#121212' },
  surfaceRaised: { default: '#f2f2f0', [DARK]: '#1e1e1e' },
  ink: { default: '#1a1a1a', [DARK]: '#ececec' },
  inkMuted: { default: '#4d4d4d', [DARK]: '#b3b3b3' },
  rule: { default: '#8a8a8a', [DARK]: '#6b6b6b' },
  focus: { default: '#0b57d0', [DARK]: '#8ab4f8' },
});

export const type = stylex.defineVars({
  family: 'ui-monospace, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
  bodyFamily: 'system-ui, -apple-system, "Segoe UI", Helvetica, Arial, sans-serif',
  size: '0.875rem',
  sizeSmall: '0.75rem',
  sizeTitle: '1.125rem',
  lineHeight: '1.4',
  tracking: '0.04em',
});

export const space = stylex.defineVars({
  xs: '0.25rem',
  sm: '0.5rem',
  md: '0.75rem',
  lg: '1.25rem',
  measure: '72rem',
});
