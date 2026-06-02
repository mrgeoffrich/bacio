import { useEffect, useRef, useState } from 'react';
import MarkdownView from '../../lib/markdownView';
import { NotionEditor } from '../editor/NotionEditor';

// InlineDescriptionEditor is the issue-description surface. By default it
// renders the body as read-only markdown through <MarkdownView> (the
// canonical React read renderer — see docs/markdown-rendering.md) with an
// Edit button; clicking Edit swaps in the rich TipTap editor (NotionEditor,
// the same engine the Documents screen uses) with Save and Cancel controls.
// BACI-339 reintroduced this click-to-edit toggle (anticipated by BACI-292's
// "fall back to a click-to-edit toggle" note) so the description isn't an
// always-mounted ProseMirror instance and so users get an explicit Cancel.
//
// Save persists the local buffer via onSave (workspace → App →
// api.updateIssueDescription) and returns to the read view; Cancel discards
// the buffer and returns to the read view without a round-trip.
//
// Stale-save fix (BACI-292 #3): the local `draft` buffer is authoritative
// after a save. We track `savedBaseline` (the last persisted body) and
// only resync `draft ← description` when the editor is idle AND an
// external change actually landed (description !== savedBaseline) — so a
// 10s poll mid-edit never stomps the buffer, and the just-saved body keeps
// rendering until a fresh brief catches up to it. While the buffer is
// dirty we keep `descEditing` true (onEditingChange) so App's poll-refresh
// guard preserves it too.
//
// readOnly (taken/waiting issues) renders the same <MarkdownView> read view
// with no Edit affordance — one renderer, visually consistent with the
// unlocked read view, and no ProseMirror mount on locked issues (BACI-339).
type InlineDescriptionEditorProps = {
  description: string;
  readOnly: boolean;
  onSave: (body: string) => void | Promise<void>;
  onEditingChange?: (editing: boolean) => void;
};

export default function InlineDescriptionEditor({
  description,
  readOnly,
  onSave,
  onEditingChange,
}: InlineDescriptionEditorProps) {
  // editing gates the rich editor: false → read-only <MarkdownView>, true →
  // NotionEditor with Save/Cancel. Always false for readOnly issues.
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(description ?? '');
  // The last body we know is persisted: seeded from the prop, advanced to
  // `draft` on a successful save, and re-seeded from the prop when an
  // external change lands while the editor is idle.
  const [savedBaseline, setSavedBaseline] = useState(description ?? '');
  const [saving, setSaving] = useState(false);

  const dirty = draft !== savedBaseline;

  // After a Save we are authoritative until the parent's `description`
  // prop catches up to the body we just persisted. The 10s brief poll is
  // both lagging (it can re-deliver the pre-save body for a beat) and —
  // because App guards the poll with `descEditing` — capable of
  // substituting the *previous* description back onto a mid-flight refresh.
  // Either way, adopting the incoming prop here would flicker the editor
  // back to the old body. So we hold this flag until the prop equals the
  // saved baseline, ignoring any stale value in between.
  const awaitingPropCatchupRef = useRef(false);

  // Keep the previous dirty value so we only fire onEditingChange on a
  // transition, not on every render.
  const wasDirtyRef = useRef(false);
  useEffect(() => {
    if (dirty !== wasDirtyRef.current) {
      wasDirtyRef.current = dirty;
      onEditingChange?.(dirty);
    }
  }, [dirty, onEditingChange]);

  // Adopt an external description change (a CLI/API edit, or another
  // surface) only when the editor is clean and not waiting for a save to
  // propagate — a poll landing mid-edit, or a stale post-save poll, must
  // not overwrite the user's buffer.
  useEffect(() => {
    const next = description ?? '';
    if (awaitingPropCatchupRef.current) {
      // Only stop ignoring the prop once it matches what we saved — that
      // confirms the persisted body has made it back through the brief.
      if (next === savedBaseline) awaitingPropCatchupRef.current = false;
      return;
    }
    if (!dirty && next !== savedBaseline) {
      setSavedBaseline(next);
      setDraft(next);
    }
  }, [description, dirty, savedBaseline]);

  const startEdit = () => {
    if (readOnly) return;
    // Seed the editor buffer from the body the read view is showing.
    setDraft(description ?? '');
    setSavedBaseline(description ?? '');
    setEditing(true);
  };

  // Cancel discards the buffer: reset draft to the saved baseline (clearing
  // `dirty`, which fires onEditingChange(false) so App's poll-guard releases
  // and the read view resyncs to the server body) and leave edit mode.
  const cancelEdit = () => {
    setDraft(savedBaseline);
    setEditing(false);
  };

  const commitSave = async () => {
    setSaving(true);
    // Hold the editor authoritative across the post-save poll storm.
    awaitingPropCatchupRef.current = true;
    try {
      await onSave(draft);
      // The local buffer is now the persisted body — keep it authoritative
      // so the new content renders immediately, no waiting for the poll.
      setSavedBaseline(draft);
      setEditing(false);
    } catch {
      // Save failed (onSave reports its own error) — drop the guard so a
      // subsequent poll can resync the editor to the unchanged server body,
      // and stay in edit mode so the user can retry without losing the buffer.
      awaitingPropCatchupRef.current = false;
    } finally {
      setSaving(false);
    }
  };

  // Read view — shared by readOnly (taken/waiting) and unlocked-not-editing.
  // The unlocked variant carries the Edit button; readOnly carries none.
  if (readOnly || !editing) {
    // While the buffer is dirty (a save is in flight) keep showing the draft
    // so the just-typed body doesn't flash back to the stale prop; otherwise
    // render the canonical description.
    const body = !readOnly && dirty ? draft : (description ?? '');
    return (
      <section className="mk-drawer-section">
        <div className="mk-drawer-label">
          Description
          {!readOnly && (
            <button
              type="button"
              className="mk-inline-edit-btn"
              onClick={startEdit}
            >
              Edit
            </button>
          )}
        </div>
        {body ? (
          <MarkdownView className="mk-markdown mk-issue-desc-read">{body}</MarkdownView>
        ) : (
          <p className="mk-drawer-text mk-meta-empty">No description.</p>
        )}
      </section>
    );
  }

  return (
    <section className="mk-drawer-section">
      <div className="mk-drawer-label">Description</div>
      <div className="mk-issue-notion-editor">
        <NotionEditor content={draft} onChange={setDraft} />
      </div>
      <div className="mk-edit-actions">
        <button
          type="button"
          className="mk-btn-secondary"
          disabled={saving}
          onClick={cancelEdit}
        >
          Cancel
        </button>
        <button
          type="button"
          className="mk-btn-primary"
          disabled={saving || !dirty}
          onClick={commitSave}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </section>
  );
}
