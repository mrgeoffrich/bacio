// Tool-renderer registry for the transcript viewer (BACI-125).
//
// Map a tool name to the React component that owns its presentation.
// `JsonBlock` is the generic fallback for everything not in the map —
// pretty-printed input on top, flattened result below, both
// collapsible. Adding a new per-tool renderer is one line here +
// one file under `tools/`.

import type React from 'react';
import BashTool from './BashTool';
import ReadTool from './ReadTool';
import EditTool from './EditTool';
import GrepGlobTool from './GrepGlobTool';
import TodoTasksTool from './TodoTasksTool';
import BacioMcpTool from './BacioMcpTool';
import JsonBlock from './JsonBlock';
import type { ToolResult } from '../types';

export type ToolRendererProps = {
  toolName: string;
  input: unknown;
  result?: ToolResult;
};

export type ToolRenderer = React.ComponentType<ToolRendererProps>;

// Plain registry keyed by tool name. The `mcp__bacio__*` tools all
// route to BacioMcpTool — the renderer branches on `toolName`
// internally so the four bacio MCP verbs share one bundle.
const REGISTRY: Record<string, ToolRenderer> = {
  Bash: BashTool,
  Read: ReadTool,
  Edit: EditTool,
  Write: EditTool,
  Grep: GrepGlobTool,
  Glob: GrepGlobTool,
  TodoWrite: TodoTasksTool,
  TaskCreate: TodoTasksTool,
  TaskUpdate: TodoTasksTool,
  mcp__bacio__reply: BacioMcpTool,
  mcp__bacio__register: BacioMcpTool,
  mcp__bacio__ask_user_question: BacioMcpTool,
  mcp__bacio__attach_transcript: BacioMcpTool,
};

export function rendererFor(toolName: string): ToolRenderer {
  return REGISTRY[toolName] ?? JsonBlock;
}

// hasBespokeRenderer reports whether a tool name has a non-fallback
// renderer. Useful for analytics or test introspection — not used by
// the viewer at render time.
export function hasBespokeRenderer(toolName: string): boolean {
  return Object.prototype.hasOwnProperty.call(REGISTRY, toolName);
}
