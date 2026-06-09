import type { NewTemplateDraft } from './useTemplateManagement';

type TemplateAddFormProps = {
  draft: NewTemplateDraft;
  onChange: (next: NewTemplateDraft) => void;
  onCommit: () => void;
};

// TemplateAddForm is the inline "New template" editor SystemSettingsSection
// reveals from the templates toolbar (BACI-364). The toolbar toggle that
// opens / collapses it stays in the shell — this component is just the form
// body, rendered when a draft exists. Create is gated on slug + name; the
// optional action label (BACI-67) derives from the name when left empty.
export default function TemplateAddForm({ draft, onChange, onCommit }: TemplateAddFormProps) {
  return (
    <div className="mk-tmpl mk-tmpl-adding">
      <div className="mk-tmpl-head">
        <span className="mk-tmpl-label">New template</span>
      </div>
      <div className="mk-tmpl-add-grid">
        <label className="mk-tmpl-add-field">
          <span>Slug</span>
          <input
            className="mk-tmpl-input"
            value={draft.slug}
            placeholder="e.g. spike"
            onChange={e => onChange({ ...draft, slug: e.target.value })}
          />
        </label>
        <label className="mk-tmpl-add-field">
          <span>Name (gerund — used by the activity pill)</span>
          <input
            className="mk-tmpl-input"
            value={draft.name}
            placeholder="e.g. Spiking"
            onChange={e => onChange({ ...draft, name: e.target.value })}
          />
        </label>
      </div>
      {/*
        BACI-67: action_label is the imperative override used by
        the dispatch dropdown buttons (kanban card + issue
        workspace shelf). When empty, the UI derives one from
        Name via the gerund→imperative rule. Optional.
      */}
      <label className="mk-tmpl-add-field">
        <span>Action label (optional · button text on dispatch dropdowns; empty derives from name)</span>
        <input
          className="mk-tmpl-input"
          value={draft.actionLabel}
          placeholder="e.g. Spike"
          onChange={e => onChange({ ...draft, actionLabel: e.target.value })}
        />
      </label>
      <textarea
        className="mk-tmpl-input"
        value={draft.body}
        rows={3}
        placeholder="Body — supports {{issue_id}}, {{issue_title}}, {{repo_prefix}}"
        onChange={e => onChange({ ...draft, body: e.target.value })}
      />
      <div className="mk-tmpl-toolbar">
        <button
          className="mk-segmented-btn is-active"
          onClick={onCommit}
          disabled={!draft.slug || !draft.name}
        >
          Create
        </button>
      </div>
    </div>
  );
}
