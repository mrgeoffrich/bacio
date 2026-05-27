// Pure decision function for BACI-240 ship-SFX gating. Lives in a
// sibling module so the Node smoketest can import it without
// pulling shipSfx.ts (which imports the asset URL via Vite — a
// runtime path Node doesn't know how to resolve).
//
// useShipSfx in shipSfx.ts re-exports this and is the integration
// point; this file is the unit-testable core.

// shouldPlayShipSfx returns true only when every gate passes:
//   - the user enabled the toggle;
//   - the OS-level `prefers-reduced-motion` is off;
//   - the Audio constructor is reachable (a non-browser env / locked
//     down profile fails here).
export function shouldPlayShipSfx(
  enabled: boolean,
  prefersReducedMotion: boolean,
  audioCtor: unknown,
): boolean {
  if (!enabled) return false;
  if (prefersReducedMotion) return false;
  if (!audioCtor) return false;
  return true;
}
