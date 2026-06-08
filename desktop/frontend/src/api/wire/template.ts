// Prompt-template-domain wire types + reshaper (BACI-358).
//
// ApiPromptTemplate is the snake_case wire shape the /settings/templates
// endpoints serialise (BACI-47/B+C, BACI-50); reshapeTemplate maps it into
// the camelCase PromptTemplateDTO the settings template manager consumes.
// ApiRestoreResponse wraps the restore-builtins payload. Moved out of
// api.http.ts so the reshape is unit testable — see ./issue.ts.

import type { PromptTemplateDTO } from '../contract';

export interface ApiPromptTemplate {
  slug: string;
  mode: string;
  label: string;
  body: string;
  default: string;
  is_builtin: boolean;
  is_default: boolean;
  concurrency_limit?: number;
  default_concurrency_limit?: number;
  concurrency_is_default?: boolean;
  // BACI-67: imperative override for the dispatch action menus.
  action_label?: string;
  default_action_label?: string;
  action_label_is_default?: boolean;
}

export interface ApiRestoreResponse {
  restored: string[];
  templates: ApiPromptTemplate[];
}

export function reshapeTemplate(t: ApiPromptTemplate): PromptTemplateDTO {
  return {
    slug: t.slug,
    mode: t.mode,
    label: t.label,
    body: t.body,
    default: t.default,
    isBuiltin: t.is_builtin,
    isDefault: t.is_default,
    concurrencyLimit: t.concurrency_limit ?? 0,
    defaultConcurrencyLimit: t.default_concurrency_limit ?? 0,
    concurrencyIsDefault: t.concurrency_is_default ?? true,
    actionLabel: t.action_label ?? '',
    defaultActionLabel: t.default_action_label ?? '',
    actionLabelIsDefault: t.action_label_is_default ?? true,
  };
}
