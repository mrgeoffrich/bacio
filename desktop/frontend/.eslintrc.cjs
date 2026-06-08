/* ESLint config for the desktop frontend (BACI-353, Phase 0).
 *
 * Deliberately narrow: it does NOT extend eslint:recommended or
 * @typescript-eslint/recommended (those would surface a flood of rules
 * across the existing tree). It codifies exactly the two things the
 * frontend-architecture refactors put most at risk and that tsc can't
 * catch on its own:
 *
 *   - react-hooks correctness (rules-of-hooks + exhaustive-deps) — the
 *     stale-closure / wrong-dep bugs every later phase risks most.
 *   - the no-`any` hard rule from .claude/rules/frontend-typescript.md.
 *
 * `strict` typing itself is enforced by `tsc` (npm run build), not here.
 * react-refresh/only-export-components keeps Fast Refresh boundaries
 * clean (advisory).
 */
module.exports = {
  root: true,
  env: {
    browser: true,
    es2022: true,
  },
  parser: '@typescript-eslint/parser',
  parserOptions: {
    ecmaVersion: 'latest',
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
  },
  plugins: ['@typescript-eslint', 'react-hooks', 'react-refresh'],
  rules: {
    'react-hooks/rules-of-hooks': 'error',
    'react-hooks/exhaustive-deps': 'error',
    '@typescript-eslint/no-explicit-any': 'error',
    'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
  },
  ignorePatterns: [
    'dist',
    'dist-web',
    'bindings',
    'node_modules',
    'scripts',
    '*.config.ts',
    '*.config.js',
    '**/*.smoketest.mjs',
  ],
};
