import Tooltip from '../Tooltip';
import type { PromptTemplateDTO } from '../../api';

type TemplateRowProps = {
  template: PromptTemplateDTO;
  draft: string;
  busy: boolean;
  onDraftChange: (slug: string, body: string) => void;
  onSaveBody: (slug: string, body: string) => void;
  onSaveActionLabel: (slug: string, actionLabel: string) => void;
  onSaveConcurrency: (slug: string, limit: number) => void;
  onRename: (template: PromptTemplateDTO) => void;
  onDelete: (slug: string) => void;
};

// TemplateRow is one prompt-template editor card in SystemSettingsSection's
// templates list (BACI-364): the body textarea (blur-committed), the
// per-template action-label + concurrency overrides, and the reset / rename /
// delete affordances. Built-in templates gain the extra "reset to default"
// buttons. All mutations are lifted to the parent via callbacks so the
// useTemplateManagement hook owns the actual writes.
export default function TemplateRow({
  template: t,
  draft,
  busy,
  onDraftChange,
  onSaveBody,
  onSaveActionLabel,
  onSaveConcurrency,
  onRename,
  onDelete,
}: TemplateRowProps) {
  const dirty = draft !== t.body;
  return (
    <div className="mk-tmpl">
      <div className="mk-tmpl-head">
        <span className="mk-tmpl-label">
          {t.label}
          {t.isBuiltin && <span className="mk-tmpl-builtin"> · built-in</span>}
          <span className="mk-tmpl-slug"> · <code>{t.slug}</code></span>
        </span>
        <div className="mk-tmpl-actions">
          {t.isBuiltin && (
            <Tooltip label="Restore the built-in default body">
              <button
                className="mk-tmpl-reset"
                disabled={busy || (t.isDefault && !dirty)}
                onClick={() => onSaveBody(t.slug, '')}
              >
                Reset body
              </button>
            </Tooltip>
          )}
          <button
            className="mk-tmpl-reset"
            disabled={busy}
            onClick={() => onRename(t)}
          >
            Rename
          </button>
          <button
            className="mk-tmpl-reset"
            disabled={busy}
            onClick={() => onDelete(t.slug)}
          >
            Delete
          </button>
        </div>
      </div>
      <textarea
        className="mk-tmpl-input"
        value={draft}
        rows={3}
        disabled={busy}
        spellCheck={false}
        onChange={e => onDraftChange(t.slug, e.target.value)}
        onBlur={() => { if (dirty) onSaveBody(t.slug, draft); }}
      />
      {/*
        BACI-67: per-template action_label override. The
        imperative form goes here; the gerund stays in
        Name (visible above) and feeds the activity-pill
        derivation on taken cards. Empty value clears the
        override and the UI derives from Name.
      */}
      <div className="mk-tmpl-states">
        <div className="mk-tmpl-states-head">
          <span className="mk-tmpl-states-label">
            Action label
            <span className="mk-tmpl-states-hint"> · imperative; rendered on dispatch dropdowns. Empty = derive from name.</span>
          </span>
          {t.isBuiltin && (
            <Tooltip label={t.defaultActionLabel ? `Reset to the built-in default ("${t.defaultActionLabel}")` : 'Clear the override'}>
              <button
                className="mk-tmpl-reset"
                disabled={busy || t.actionLabelIsDefault}
                onClick={() => onSaveActionLabel(t.slug, t.defaultActionLabel)}
              >
                Reset action label
              </button>
            </Tooltip>
          )}
        </div>
        <input
          type="text"
          className="mk-tmpl-input"
          defaultValue={t.actionLabel}
          key={`${t.slug}:action:${t.actionLabel}`}
          placeholder="e.g. Plan, Design, Implement (empty derives from name)"
          disabled={busy}
          onBlur={(e) => {
            const v = e.target.value;
            if (v !== t.actionLabel) onSaveActionLabel(t.slug, v);
          }}
        />
      </div>
      <div className="mk-tmpl-states">
        <div className="mk-tmpl-states-head">
          <span className="mk-tmpl-states-label">
            Concurrency limit
            <span className="mk-tmpl-states-hint"> · 0 = unlimited</span>
          </span>
          {t.isBuiltin && (
            <Tooltip label={`Reset to the built-in default (${t.defaultConcurrencyLimit})`}>
              <button
                className="mk-tmpl-reset"
                disabled={busy || t.concurrencyIsDefault}
                onClick={() => onSaveConcurrency(t.slug, t.defaultConcurrencyLimit)}
              >
                Reset limit
              </button>
            </Tooltip>
          )}
        </div>
        <input
          type="number"
          min={0}
          step={1}
          className="mk-tmpl-input mk-tmpl-concurrency"
          defaultValue={t.concurrencyLimit}
          key={`${t.slug}:${t.concurrencyLimit}`}
          disabled={busy}
          onBlur={(e) => {
            const n = Math.max(0, parseInt(e.target.value, 10) || 0);
            if (n !== t.concurrencyLimit) onSaveConcurrency(t.slug, n);
          }}
        />
      </div>
    </div>
  );
}
