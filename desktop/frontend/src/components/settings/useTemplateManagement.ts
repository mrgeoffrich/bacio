import { useCallback, useEffect, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import * as api from '../../api';
import type { PromptTemplateDTO } from '../../api';
import { reportError } from '../../errors';
import { useSubmodalBubble } from './useSubmodalBubble';

// EMPTY_NEW_TEMPLATE is the seed state for the "Add template" inline form.
// actionLabel (BACI-67) is optional — an empty string is the "no override,
// derive from the gerund name" sentinel the backend honours.
export type NewTemplateDraft = { slug: string; name: string; body: string; actionLabel: string };
export const EMPTY_NEW_TEMPLATE: NewTemplateDraft = { slug: '', name: '', body: '', actionLabel: '' };

// RenameDraft is the rename-overlay form state — `null` when closed.
export type RenameDraft = { slug: string; newSlug: string; newName: string };

// TemplateManagement is the surface useTemplateManagement hands the
// SystemSettingsSection shell: the template-CRUD state cluster plus the
// mutating handlers, all already wired to keep App's promptConfig in sync.
export type TemplateManagement = {
  templates: PromptTemplateDTO[];
  placeholders: string[];
  drafts: Record<string, string>;
  savingSlug: string | null;
  bacioVer: string;
  adding: NewTemplateDraft | null;
  setAdding: Dispatch<SetStateAction<NewTemplateDraft | null>>;
  renaming: RenameDraft | null;
  setRenaming: Dispatch<SetStateAction<RenameDraft | null>>;
  pendingDelete: string | null;
  setPendingDelete: Dispatch<SetStateAction<string | null>>;
  pendingRestore: boolean;
  setPendingRestore: Dispatch<SetStateAction<boolean>>;
  setDraft: (slug: string, body: string) => void;
  saveTemplate: (slug: string, body: string) => Promise<void>;
  saveConcurrency: (slug: string, limit: number) => Promise<void>;
  saveActionLabel: (slug: string, actionLabel: string) => Promise<void>;
  commitAdd: () => Promise<void>;
  commitRename: () => Promise<void>;
  commitDelete: () => Promise<void>;
  commitRestore: () => Promise<void>;
};

// useTemplateManagement bundles the prompt-template CRUD cluster carved out
// of SystemSettingsSection (BACI-364) — the densest local-state group in the
// frontend: the eight template useStates, the initial load (templates +
// placeholders + the About-band bacio version, fetched in one Promise.all),
// and the eight mutating handlers. Each mutation threads through
// onTemplatesChanged so the promptConfig up in App.tsx stays in sync without
// waiting for the Settings screen to close. The submodal-open signal that
// suppresses SettingsView's Escape-to-close is bubbled from here too, since
// every overlay it tracks (add / rename / delete / restore) is template-owned.
export function useTemplateManagement(onTemplatesChanged?: () => void): TemplateManagement {
  const [templates, setTemplates] = useState<PromptTemplateDTO[]>([]);
  const [placeholders, setPlaceholders] = useState<string[]>([]);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [savingSlug, setSavingSlug] = useState<string | null>(null);
  const [bacioVer, setBacioVer] = useState('');

  // Add-template inline form. `null` = collapsed; an object = open.
  const [adding, setAdding] = useState<NewTemplateDraft | null>(null);
  // Rename overlay state: { slug, newSlug, newName } when open.
  const [renaming, setRenaming] = useState<RenameDraft | null>(null);
  // Confirm overlays.
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const [pendingRestore, setPendingRestore] = useState(false);

  const refreshTemplates = useCallback(async () => {
    const tpls = await api.listPromptTemplates();
    setTemplates(tpls);
    setDrafts(Object.fromEntries(tpls.map(t => [t.slug, t.body])));
    return tpls;
  }, []);

  useEffect(() => {
    let cancelled = false;
    // BACI-50 closed the web-mode CRUD gap — every affordance below is
    // available in both desktop and web.
    Promise.all([refreshTemplates(), api.promptPlaceholders(), api.bacioVersion()])
      .then(([, ph, ver]) => {
        if (cancelled) return;
        setPlaceholders(ph);
        setBacioVer(ver);
      })
      .catch(err => { if (!cancelled) reportError(err, { headline: "Couldn't load templates" }); });
    return () => { cancelled = true; };
  }, [refreshTemplates]);

  // Each mutating path threads through this helper so the promptConfig
  // up in App.tsx stays in sync without waiting for the Settings screen
  // to close.
  const notifyTemplatesChanged = useCallback(() => {
    if (typeof onTemplatesChanged === 'function') onTemplatesChanged();
  }, [onTemplatesChanged]);

  // Live edit of one template's body draft, before the blur-commit.
  const setDraft = useCallback((slug: string, body: string) => {
    setDrafts(prev => ({ ...prev, [slug]: body }));
  }, []);

  const saveTemplate = useCallback(async (slug: string, body: string) => {
    setSavingSlug(slug);
    try {
      const updated = await api.savePromptTemplate(slug, body);
      setTemplates(prev => prev.map(t => (t.slug === slug ? updated : t)));
      setDrafts(prev => ({ ...prev, [slug]: updated.body }));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't save template body" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  // BACI-51: persist a template's per-(repo, slug) in-flight cap. The
  // matcher reads this column on every tick to decide whether to bind
  // another queued dispatch. 0 = unlimited; built-in ship seeds to 1
  // so merging serialises. Validation (>=0) lives in the store.
  const saveConcurrency = useCallback(async (slug: string, limit: number) => {
    setSavingSlug(slug);
    try {
      const updated = await api.savePromptConcurrency(slug, limit);
      setTemplates(prev => prev.map(t => (t.slug === slug ? updated : t)));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't save concurrency limit" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  // BACI-67: persist the imperative action_label override — the verb the
  // dispatch action menus render on the kanban-card + issue-workspace
  // dropdowns. An empty string clears the override; the UI then derives
  // a default from the gerund display name (the validator on the Go side
  // accepts the empty value as the "no override" sentinel).
  const saveActionLabel = useCallback(async (slug: string, actionLabel: string) => {
    setSavingSlug(slug);
    try {
      const updated = await api.savePromptActionLabel(slug, actionLabel);
      setTemplates(prev => prev.map(t => (t.slug === slug ? updated : t)));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't save action label" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  const commitAdd = useCallback(async () => {
    if (!adding) return;
    setSavingSlug(adding.slug || '__new__');
    try {
      await api.addPromptTemplate(adding.slug, adding.name, adding.body, adding.actionLabel || '');
      setAdding(null);
      await refreshTemplates();
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't add template" });
    } finally {
      setSavingSlug(null);
    }
  }, [adding, notifyTemplatesChanged, refreshTemplates]);

  const commitRename = useCallback(async () => {
    if (!renaming) return;
    setSavingSlug(renaming.slug);
    try {
      await api.renamePromptTemplate(renaming.slug, renaming.newSlug, renaming.newName);
      setRenaming(null);
      await refreshTemplates();
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't rename template" });
    } finally {
      setSavingSlug(null);
    }
  }, [renaming, notifyTemplatesChanged, refreshTemplates]);

  const commitDelete = useCallback(async () => {
    if (!pendingDelete) return;
    setSavingSlug(pendingDelete);
    try {
      await api.deletePromptTemplate(pendingDelete);
      setPendingDelete(null);
      await refreshTemplates();
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't delete template" });
    } finally {
      setSavingSlug(null);
    }
  }, [pendingDelete, notifyTemplatesChanged, refreshTemplates]);

  const commitRestore = useCallback(async () => {
    setPendingRestore(false);
    setSavingSlug('__restore__');
    try {
      const refreshed = await api.restoreBuiltinPromptTemplates();
      setTemplates(refreshed);
      setDrafts(Object.fromEntries(refreshed.map(t => [t.slug, t.body])));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't restore built-in templates" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  // Any submodal open (add / rename / delete / restore confirm) suppresses
  // the page-level Escape close in SettingsView. The overlays are all
  // template-owned, so the bubble lives with their state.
  const subModalOpen = !!(adding || renaming || pendingDelete || pendingRestore);
  useSubmodalBubble(subModalOpen);

  return {
    templates,
    placeholders,
    drafts,
    savingSlug,
    bacioVer,
    adding,
    setAdding,
    renaming,
    setRenaming,
    pendingDelete,
    setPendingDelete,
    pendingRestore,
    setPendingRestore,
    setDraft,
    saveTemplate,
    saveConcurrency,
    saveActionLabel,
    commitAdd,
    commitRename,
    commitDelete,
    commitRestore,
  };
}
