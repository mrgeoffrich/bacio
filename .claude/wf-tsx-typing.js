export const meta = {
  name: 'tsx-strict-typing',
  description: 'Drive desktop/frontend tsc --noEmit to zero under full strict; one fixer agent per error-file, loop until clean',
  phases: [
    { title: 'Type', detail: 'map tsc errors, then one fixer per error-file per round' },
    { title: 'Verify', detail: 'final tsc gate' },
  ],
}

const FE = '/Users/geoff/Repos/bacio/desktop/frontend'
const ERRFILE = '/tmp/baci346-tsc.txt'

const CONVENTIONS = `You are typing one file in a JSX->TSX strict-typing migration of the bacio React frontend
(${FE}/src). The files were renamed .jsx->.tsx and tsconfig now has strict + noImplicitAny:true
+ noUnusedParameters:true + noUnusedLocals:true, surfacing implicit-any and inference errors.

GOAL: make tsc happy for your assigned file with REAL strong types. This is a PURE TYPING change.

ABSOLUTE RULES:
- Edit ONLY your assigned file. Do NOT touch any other file. If a JSX call-site error is rooted in
  ANOTHER component's incomplete Props, report it in your summary and leave it — a later round fixes it.
- NEVER change runtime behaviour. Only add type annotations, add null-guards, remove dead imports/params.
- NEVER use 'any' or 'as any'. No '@ts-ignore'. Use precise types or 'unknown' + narrowing if truly needed.
- Do NOT edit api.ts, api.http.ts, the generated bindings/, or tsconfig.json — they are correct.

HOW TO TYPE:
- Domain types come from the api seam and generated bindings — REUSE them, never redefine. Import with
  'import type { Board, IssueDetail, BoardCard, WaitingState, ... } from "../api"' (adjust ../ depth to
  your file's location; components/ uses '../api', components/issue|settings/ uses '../../api', lib/ uses
  '../api', src root uses './api'). The full list of exported types lives in ${FE}/src/api.ts.
- Component props: define a named 'type FooProps = { ... }' ABOVE the component, then
  'function Foo({ ... }: FooProps)'. Do NOT use React.FC. To get the prop types EXACTLY right, grep every
  call site first:  grep -rn '<Foo' ${FE}/src   — type each prop from what callers actually pass, and mark
  a prop optional with '?' if any call site omits it or passes undefined. Callback props get full signatures
  e.g. 'onPick: (prefix: string) => void'. children: 'React.ReactNode'.
- useState: add an explicit generic whenever the initial value is [] / null / {} and the real shape is
  something else, e.g. useState<BoardCard[]>([]), useState<IssueDetail | null>(null),
  useState<Record<string, boolean>>({}), useState<Set<string>>(new Set()). Infer the element type from what
  is later assigned (e.g. the return type of the api.* call).
- Event handlers / refs: rely on inference (onChange={(e) => ...} infers correctly); add React.MouseEvent /
  React.ChangeEvent<HTMLInputElement> / useRef<HTMLDivElement>(null) only where inference fails.
- noUnusedParameters: prefix an intentionally-unused param with '_' (e.g. _e, _i, _idx). Established
  convention — see src/wails-stub.ts. Remove genuinely-dead imports/locals that tsc flags (TS6133),
  including an unused default 'import React from "react"' (keep it only if React.X is actually referenced).
- Null safety (strictNullChecks): Wails binding fields are often 'field?: T | null'. Prefer optional
  chaining / narrowing that preserves CURRENT runtime behaviour (e.g. board?.syncEnabled, with the same
  fallback the surrounding code implies). Use a non-null assertion '!' ONLY where the existing code already
  relies on a runtime invariant. Do not turn a would-be runtime throw into silent undefined unless the code
  clearly already tolerated it.

CONVENTION REFERENCES (read for style): src/lib/transcript/EventCard.tsx, src/components/editor/NotionEditor.tsx.
ErrorBoundary.tsx is a class component -> 'class ErrorBoundary extends React.Component<Props, State>'.

WORKFLOW FOR YOUR FILE:
1. Read your assigned file fully.
2. Read your file's current errors:  grep -F "<RELPATH>" ${ERRFILE}
   (RELPATH is given below, e.g. src/components/Topbar.tsx)
3. Read api.ts / the relevant bindings / call sites as needed (READ-ONLY) to choose correct types.
4. Edit ONLY your file to clear every listed error following the rules above.
Do not run tsc yourself (other agents are editing concurrently; the driver re-checks after the round).
Return ONE line: relpath, count of errors you addressed, and any cross-file residue you could not fix.`

const MAP_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    total: { type: 'number' },
    files: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          relpath: { type: 'string', description: 'e.g. src/components/Topbar.tsx' },
          count: { type: 'number' },
        },
        required: ['relpath', 'count'],
      },
    },
  },
  required: ['total', 'files'],
}

async function mapErrors(round) {
  return await agent(
    `Run exactly:\n  cd ${FE} && npx tsc --noEmit 2>&1 | grep "error TS" > ${ERRFILE}; wc -l < ${ERRFILE}\n` +
    `Then produce the per-file breakdown:\n` +
    `  sed -E 's/\\(([0-9]+),([0-9]+)\\):.*//' ${ERRFILE} | sort | uniq -c | sort -rn\n` +
    `Return {total, files:[{relpath, count}]} where total is the line count of ${ERRFILE} and each relpath is ` +
    `the source path exactly as tsc prints it (begins with "src/"). Include EVERY file that has at least one ` +
    `error. Do NOT edit anything; this is a read/measure task only.`,
    { label: `tsc map r${round}`, phase: 'Type', schema: MAP_SCHEMA },
  )
}

function fixPrompt(f) {
  return `${CONVENTIONS}\n\n=== YOUR ASSIGNED FILE ===\nRELPATH: ${f.relpath}\nABSOLUTE: ${FE}/${f.relpath}\n` +
    `This file currently has ${f.count} tsc error(s). See them with:  grep -F "${f.relpath}" ${ERRFILE}\n` +
    `Clear all of them. Edit ONLY this file.`
}

phase('Type')
let prev = Infinity
let last = null
for (let round = 1; round <= 6; round++) {
  const map = await mapErrors(round)
  last = map
  log(`Round ${round}: ${map.total} errors across ${map.files.length} files`)
  if (map.total === 0) break
  if (map.total >= prev) { log(`No progress vs prev ${prev} — stopping for manual residue.`); break }
  prev = map.total
  await parallel(map.files.map((f) => () => agent(fixPrompt(f), { label: `fix:${f.relpath.replace(/^src\//, '')}`, phase: 'Type' })))
}

phase('Verify')
const final = await mapErrors('final')
return { remaining: final.total, files: final.files }
