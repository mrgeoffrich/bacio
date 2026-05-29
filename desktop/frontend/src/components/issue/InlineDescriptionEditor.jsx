import React, { useEffect, useRef, useState } from 'react';
import { NotionEditor } from '../editor/NotionEditor';

// InlineDescriptionEditor is the issue-description edit surface. BACI-292
// swapped the plain <textarea> for the rich TipTap editor the Documents
// screen uses (NotionEditor) so issue bodies get WYSIWYG markdown editing
// — headings, lists, bold/italic, inline code, tables — consistent with
// DocsView. The editor is always-on: there's no read/edit toggle. A Save
// button persists the local buffer via onSave (workspace → App →
// api.updateIssueDescription).
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
// readOnly (taken/waiting issues) renders NotionEditor read-only — no
// toolbar, no Save — fed straight from the prop.
export default function InlineDescriptionEditor({
  description,
  readOnly,
  onSave,
  onEditingChange,
}) {
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

  const commitSave = async () => {
    setSaving(true);
    // Hold the editor authoritative across the post-save poll storm.
    awaitingPropCatchupRef.current = true;
    try {
      await onSave(draft);
      // The local buffer is now the persisted body — keep it authoritative
      // so the new content renders immediately, no waiting for the poll.
      setSavedBaseline(draft);
    } catch {
      // Save failed (onSave reports its own error) — drop the guard so a
      // subsequent poll can resync the editor to the unchanged server body.
      awaitingPropCatchupRef.current = false;
    } finally {
      setSaving(false);
    }
  };

  if (readOnly) {
    return (
      <section className="mk-drawer-section">
        <div className="mk-drawer-label">Description</div>
        {description ? (
          <div className="mk-issue-notion-editor is-readonly">
            <NotionEditor content={description} onChange={() => {}} readOnly />
          </div>
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
