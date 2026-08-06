import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Routes, Route, Navigate, useNavigate, useLocation } from 'react-router';
import Topbar, { navForKind } from './components/Topbar';
import KanbanBoard from './components/kanban/KanbanBoard';
import DocsView from './components/DocsView';
import FeaturesView from './components/FeaturesView';
import AgentsView from './components/AgentsView';
import HistoryView from './components/HistoryView';
import MonitorView from './components/MonitorView';
import TranscriptRoute from './components/TranscriptRoute';
import PipelineView from './components/PipelineView';
import ProcessEditor from './components/ProcessEditor';
import IssueWorkspace from './components/IssueWorkspace';
import CommandPalette from './components/CommandPalette';
import IssueComposer from './components/IssueComposer';
import RepoNotFound from './components/RepoNotFound';
import SettingsView from './components/SettingsView';
import ErrorBoundary from './components/ErrorBoundary';
import ErrorModal from './components/ErrorModal';
import { Provider as TooltipProvider } from '@radix-ui/react-tooltip';
import { LazyMotion, domMax, LayoutGroup } from 'motion/react';
import { reportError } from './errors';
import * as api from './api';
import type { BoardCard } from './api';
import { viewPath, issuePath, homeView } from './lib/routes';
import { PreferencesProvider, usePreferences } from './state/PreferencesProvider';
import { RepoProvider, useActiveRepo } from './state/RepoProvider';
import { AgentsProvider } from './state/AgentsProvider';
import { CardsProvider, useCards } from './state/CardsProvider';

// BACI-361: App.tsx is now a shell. The cards / agents / brief / preferences
// state that used to live here moved into the providers under src/state/;
// App() composes the provider tree and Shell() holds the ephemeral overlay
// flags, the keyboard-shortcut effect, the loading / no-repos / settings /
// prefix-unknown branch, the <Routes>, and the overlay siblings. Everything
// else reads its data from the context hooks.

// isEditingTarget reports whether a keystroke landed in something the user is
// typing into — a form field or the contenteditable doc editor — so global
// hotkeys can stand down rather than hijack the keypress.
function isEditingTarget(el: EventTarget | null) {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
}

// Shell sits inside every provider, so it reads global data from the context
// hooks and owns only the ephemeral overlay flags (palette / settings /
// composer), the global keyboard shortcuts, and the page routing.
function Shell() {
  const navigate = useNavigate();
  const location = useLocation();
  const {
    boards,
    loading,
    activeBoard,
    prefixUnknown,
    legacyRedirectTarget,
    activeView,
    openCard,
    openIssue,
    closeIssue,
  } = useActiveRepo();
  const { refreshPromptConfig } = usePreferences();
  const { setCards, refreshCards } = useCards();

  const [paletteOpen, setPaletteOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  // BACI-248: SettingsView owns the Sync section internally; the topbar Sync
  // pill opens Settings with `initialSection='sync'` preselected. The
  // preselect resets to null whenever the view closes so the next plain "open
  // Settings" lands on System.
  const [settingsInitialSection, setSettingsInitialSection] = useState<string | null>(null);
  // BACI-166: the "+ from prompt" composer is a sibling modal flag — reached
  // via the Topbar's `+` button or the ⌘N shortcut.
  const [composerOpen, setComposerOpen] = useState(false);

  // The active repo's kind drives two things the shell owns: which nav the
  // digit hotkeys map onto (a workspace hides the Agentic Pipeline entry, so
  // the digits have to shift with the buttons — see Topbar.navForKind), and
  // which view the `/:prefix/*` catch-all lands on.
  const activeKind = boards.find(b => b.prefix === activeBoard)?.kind;
  const navItems = useMemo(() => navForKind(activeKind), [activeKind]);

  // BACI-203 / BACI-285: derive the open issue key from the route — null when
  // we're not on an issue workspace. Drives the Escape-to-close branch below.
  const openIssueKey = (() => {
    const m = location.pathname.match(/^\/[^/]+\/issues\/([^/]+)$/);
    return m ? m[1] : null;
  })();

  const closeSettings = useCallback(() => {
    setSettingsOpen(false);
    setSettingsInitialSection(null);
  }, []);
  // BACI-248: the topbar Sync pill opens Settings on its Sync section.
  const openSync = useCallback(() => {
    setSettingsInitialSection('sync');
    setSettingsOpen(true);
  }, []);

  // Reload the prompt config whenever the Settings view closes — a state-gate
  // edit there changes which prompts each card offers. The ref guards against
  // the mount-time false→false non-transition. settingsOpen is shell-local,
  // so this effect stays here.
  const prevSettingsOpen = useRef(false);
  useEffect(() => {
    if (prevSettingsOpen.current && !settingsOpen) refreshPromptConfig();
    prevSettingsOpen.current = settingsOpen;
  }, [settingsOpen, refreshPromptConfig]);

  // BACI-166 / BACI-332 / BACI-374: composer success handler — optimistically
  // prepend the new card, then route. With auto-run on (the composer default)
  // the server already armed the card in one call, so there is nothing to do
  // but route to the Pipeline, where its progress renders — from any view,
  // because that is genuinely where the card now lives. With auto-run off we
  // keep the pre-existing behaviour verbatim: from the Pipeline screen,
  // auto-scope it (pipe + Scope job on Auto via the scope-shelve chain) and
  // route to the Pipeline; off the Pipeline, route into the new card's
  // workspace. On a mid-chain failure of that older sequence the steps before
  // it already persisted and the refresh leaves a coherent partial state.
  const onComposerCreated = useCallback(async (newCard: BoardCard, autoRan: boolean) => {
    if (!newCard || !newCard.key) return;
    setCards(cs => [{ ...newCard }, ...cs]);
    setSettingsOpen(false);
    setSettingsInitialSection(null);
    if (autoRan) {
      refreshCards({ silent: true });
      navigate(viewPath(activeBoard, 'pipeline'));
      return;
    }
    if (activeView === 'pipeline') {
      try {
        await api.setIssueState(activeBoard, newCard.key, 'in_pipeline');
        await api.setCardProcess(activeBoard, newCard.key, { process: 'scope-shelve' });
        await api.setEngineMode(activeBoard, newCard.key, 'auto');
      } catch (err) {
        reportError(err, { headline: "Couldn't scope the new card" });
      } finally {
        refreshCards({ silent: true });
      }
      navigate(viewPath(activeBoard, 'pipeline'));
      return;
    }
    navigate(issuePath(activeBoard, newCard.key));
  }, [navigate, activeBoard, activeView, refreshCards, setCards]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen(true);
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'n') {
        // BACI-166: ⌘N opens the IssueComposer. Skip when typing into a field
        // (so the OS-level new-document muscle memory doesn't fight us inside
        // an editor). Guard on a real repo prefix — the composer needs one.
        if (isEditingTarget(e.target)) return;
        if (!activeBoard || activeBoard === 'all') return;
        e.preventDefault();
        setComposerOpen(true);
      } else if (e.key === 'Escape') {
        // Palette + Settings are still hand-rolled; the workspace closes here
        // when nothing else is in front of it. The composer closes via Radix's
        // built-in onOpenChange. SettingsView handles its own page-level
        // Escape internally (BACI-248) so it can suppress the close while a
        // sub-modal is open; the branch below is the no-op safety net.
        if (paletteOpen) {
          setPaletteOpen(false);
        } else if (openIssueKey && !isEditingTarget(e.target)) {
          closeIssue();
        } else if (settingsOpen) {
          setSettingsOpen(false);
          setSettingsInitialSection(null);
        }
      } else if (!e.metaKey && !e.ctrlKey && !e.altKey && e.key >= '1' && e.key <= '9') {
        // Digit keys jump between nav views, like the TUI's tab shortcuts —
        // unless the user is typing into a field or the doc editor. Skip when
        // no real repo is active so we don't navigate to a prefix-less path.
        if (isEditingTarget(e.target)) return;
        if (!activeBoard) return;
        const idx = Number(e.key) - 1;
        if (idx < navItems.length) {
          navigate(viewPath(activeBoard, navItems[idx].view));
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [paletteOpen, settingsOpen, openIssueKey, activeBoard, navItems, closeIssue, navigate]);

  return (
    // BACI-268: tag the shell on the Pipeline route so the bottom-right
    // Activity tray lifts above the drag-to-cancel bin; other routes keep the
    // tray in the corner.
    <div className={`mk-app${activeView === 'pipeline' ? ' is-pipeline' : ''}`}>
      {/* BACI-193: wrap Topbar + main view in one LayoutGroup so the
          ship-flourish layoutId match can cross from the pipeline card to the
          Shipped pill destination in the topbar. */}
      <LayoutGroup id="kanban">
        <Topbar
          onBeforeNavigate={() => { setSettingsOpen(false); setSettingsInitialSection(null); }}
          onOpenSettings={() => { setSettingsInitialSection(null); setSettingsOpen(true); }}
          onOpenSync={openSync}
        />
        {loading ? (
          <div className="mk-app-state">Loading…</div>
        ) : boards.length === 0 ? (
          // BACI-285: no repos registered yet — there's no prefix to route to,
          // so render an empty state (and skip <Routes>, whose `/` redirect
          // would loop on an empty fallback).
          <div className="mk-app-state">No repositories yet — add one from the repo picker above.</div>
        ) : settingsOpen ? (
          <ErrorBoundary headline="Something went wrong in Settings" label="The Settings view crashed">
            <SettingsView onClose={closeSettings} initialSection={settingsInitialSection} />
          </ErrorBoundary>
        ) : prefixUnknown ? (
          // BACI-285: the URL's first segment is a genuinely-unknown repo
          // prefix. Render the hard-404 screen rather than stranding the user
          // on a blank board. Legacy page links soft-redirect through the
          // <Routes> `*` route instead.
          <RepoNotFound />
        ) : (
          <Routes>
            {/* BACI-285: every page route is scoped to the active repo's prefix
                (`/<PREFIX>/<page>`). The providers read the prefix off
                location.pathname, so the page elements don't need the route
                param — the `:prefix` segment only anchors the route tree. */}
            <Route path="/" element={<Navigate to={legacyRedirectTarget} replace />} />
            <Route
              path="/:prefix/pipeline"
              element={
                <ErrorBoundary headline="Something went wrong in Pipeline" label="The Pipeline view crashed">
                  <PipelineView onOpenComposer={() => setComposerOpen(true)} />
                </ErrorBoundary>
              }
            />
            {/* BACI-294: the full-screen Edit Process editor reads the card's
                live chain off the cached cards (useCards) and persists the
                re-ordered pending tail. */}
            <Route
              path="/:prefix/pipeline/:key/process"
              element={
                <ErrorBoundary headline="Something went wrong in Edit Process" label="The Edit Process view crashed">
                  <ProcessEditor />
                </ErrorBoundary>
              }
            />
            {/* The Kanban board — the human work axis, orthogonal to the
                Agentic Pipeline's issue states. `viewPath('board')` has
                always emitted `/<prefix>/issues` (the URL alias predates the
                pivot); the `:key` child route below is the issue workspace
                that opens off it. */}
            <Route
              path="/:prefix/issues"
              element={
                <ErrorBoundary headline="Something went wrong in Kanban" label="The Kanban view crashed">
                  <KanbanBoard />
                </ErrorBoundary>
              }
            />
            {/* The workspace route self-sources its brief + writes via
                useOpenIssue (route-scoped, tears down on unmount). */}
            <Route
              path="/:prefix/issues/:key"
              element={
                <ErrorBoundary headline="Something went wrong in the issue view" label="The issue view crashed">
                  <IssueWorkspace />
                </ErrorBoundary>
              }
            />
            <Route
              path="/:prefix/features"
              element={
                <ErrorBoundary headline="Something went wrong in Features" label="The Features view crashed">
                  <FeaturesView />
                </ErrorBoundary>
              }
            />
            <Route
              path="/:prefix/features/:slug"
              element={
                <ErrorBoundary headline="Something went wrong in Features" label="The Features view crashed">
                  <FeaturesView />
                </ErrorBoundary>
              }
            />
            <Route
              path="/:prefix/documents"
              element={
                <ErrorBoundary headline="Something went wrong in Docs" label="The Docs view crashed">
                  <DocsView activeBoard={activeBoard} onOpenIssue={openIssue} />
                </ErrorBoundary>
              }
            />
            <Route
              path="/:prefix/documents/:slug"
              element={
                <ErrorBoundary headline="Something went wrong in Docs" label="The Docs view crashed">
                  <DocsView activeBoard={activeBoard} onOpenIssue={openIssue} />
                </ErrorBoundary>
              }
            />
            <Route
              path="/:prefix/agents"
              element={
                <ErrorBoundary headline="Something went wrong in Agents" label="The Agents view crashed">
                  <AgentsView />
                </ErrorBoundary>
              }
            />
            <Route
              path="/:prefix/history"
              element={
                <ErrorBoundary headline="Something went wrong in History" label="The History view crashed">
                  <HistoryView activeBoard={activeBoard} />
                </ErrorBoundary>
              }
            />
            <Route
              path="/:prefix/monitor"
              element={
                <ErrorBoundary headline="Something went wrong in Monitor" label="The Monitor view crashed">
                  <MonitorView activeBoard={activeBoard} />
                </ErrorBoundary>
              }
            />
            {/* BACI-322: the Transcripts sub-tab is the same Monitor shell — it
                derives the active sub-tab from the URL. */}
            <Route
              path="/:prefix/monitor/transcripts"
              element={
                <ErrorBoundary headline="Something went wrong in Monitor" label="The Monitor view crashed">
                  <MonitorView activeBoard={activeBoard} />
                </ErrorBoundary>
              }
            />
            {/* BACI-322: the deep-linkable full-transcript page for one dispatch. */}
            <Route
              path="/:prefix/monitor/transcript/:id"
              element={
                <ErrorBoundary headline="Something went wrong in the transcript view" label="The transcript view crashed">
                  <TranscriptRoute />
                </ErrorBoundary>
              }
            />
            {/* Catch-all: an unknown page under a valid prefix lands on that
                repo's home board — the Agentic Pipeline for a git repo, the
                Kanban for a workspace (which has no Pipeline nav entry to
                land on); a prefix-less / stale single-segment legacy path has
                an empty activeBoard and falls through to the legacy redirect.
                `/:prefix/*` outranks a bare `*` in react-router. */}
            <Route
              path="/:prefix/*"
              element={
                <Navigate
                  to={activeBoard ? viewPath(activeBoard, homeView(activeKind)) : legacyRedirectTarget}
                  replace
                />
              }
            />
            <Route path="*" element={<Navigate to={legacyRedirectTarget} replace />} />
          </Routes>
        )}
        <ErrorBoundary headline="Something went wrong in the command palette" label="The command palette crashed">
          <CommandPalette
            open={paletteOpen}
            onClose={() => setPaletteOpen(false)}
            onPick={(card) => {
              // Dismiss Settings (the palette can open over it) before
              // routing to the picked card's workspace.
              setSettingsOpen(false);
              setSettingsInitialSection(null);
              openCard(card);
            }}
          />
        </ErrorBoundary>
        {/* BACI-166: + from prompt composer. Sibling of CommandPalette /
            ErrorModal so it overlays whatever view is current. */}
        <ErrorBoundary headline="Something went wrong in the issue composer" label="The issue composer crashed">
          <IssueComposer
            open={composerOpen}
            onClose={() => setComposerOpen(false)}
            onCreated={onComposerCreated}
          />
        </ErrorBoundary>
        <ErrorModal />
      </LayoutGroup>
    </div>
  );
}

export default function App() {
  return (
    <TooltipProvider delayDuration={250} skipDelayDuration={150}>
      <LazyMotion features={domMax} strict>
        <PreferencesProvider>
          <RepoProvider>
            <AgentsProvider>
              <CardsProvider>
                <Shell />
              </CardsProvider>
            </AgentsProvider>
          </RepoProvider>
        </PreferencesProvider>
      </LazyMotion>
    </TooltipProvider>
  );
}
