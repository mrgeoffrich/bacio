import { useEffect } from 'react';

// useSubmodalBubble bubbles the "a child submodal is open" signal up so the
// SettingsView shell can suppress its own Escape-to-close listener while a
// child modal (rename / delete / restore confirm, or the inline add form)
// is mounted — Radix Dialog catches Escape first and dismisses just the
// modal, leaving the section pane mounted.
//
// BACI-248: we hang it off the parent via a custom event because the props
// that arrive at the section are App-owned; a callback prop is the cleaner
// door but adds another seam — the event keeps the parent shell ignorant of
// which section is mounted. (BACI-364: lifted out of SystemSettingsSection.)
export function useSubmodalBubble(open: boolean): void {
  useEffect(() => {
    window.dispatchEvent(new CustomEvent('mk-settings-submodal', { detail: { open } }));
    return () => {
      if (open) {
        window.dispatchEvent(new CustomEvent('mk-settings-submodal', { detail: { open: false } }));
      }
    };
  }, [open]);
}
