// Keep navigation styles with its DOM in both the app and embedded stories.
export const navigationStyles = `
.topbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 0 8px;
  position: sticky;
  top: 0;
  background: linear-gradient(var(--bg) 70%, transparent);
  z-index: 5;
}
.brand { display: inline-flex; align-items: center; gap: 8px; }
.brand-mark { width: 22px; height: 22px; border-radius: 6px; }
.brand-name { display: inline-flex; color: var(--ink); }
.brand-name svg { display: block; width: auto; height: 19px; }
/* Navigation stays separate from each thread’s read-aloud control. */
.topbar .nav { display: inline-flex; flex-direction: row; flex-wrap: nowrap; align-items: center; justify-content: center; gap: 6px; font-size: 0.9375rem; white-space: nowrap; }
.topbar:not(.compact) { gap: 6px; }
.topbar:not(.compact) .nav { padding: 4px 6px; }
.topbar .nav > .nav-icon { display: inline-flex; flex: 0 0 auto; }
/* Navigation reads in ink; accent icons mark the primary actions. */
.topbar .nav:is(.nav-search, .nav-schedules, .nav-personas, .nav-settings) { color: var(--ink); }
.topbar .nav:is(.nav-search, .nav-schedules, .nav-personas, .nav-settings) .nav-icon { color: var(--accent); }
.topbar .nav:is(.nav-search, .nav-schedules, .nav-personas, .nav-settings):hover { background: var(--accent-soft); color: var(--ink); }
.topbar .nav > .nav-label { display: inline; white-space: nowrap; }
.topbar .nav-icon svg { display: block; }
/* Compact layout follows the header's measured width, including in Storybook. */
.topbar.compact { gap: 4px; flex-wrap: nowrap; justify-content: flex-end; }
.topbar.compact .brand { margin-right: auto; }
.topbar.compact > .spacer, .topbar.compact .brand-name { display: none; }
.topbar.compact .nav { flex: 0 0 44px; width: 44px; height: 44px; padding: 0; }
.nav-secondary { display: flex; align-items: center; gap: 4px; }
.nav-secondary a { text-decoration: none; }
.nav-secondary .nav { color: var(--ink); }
.nav-more { position: relative; flex: 0 0 auto; }
.topbar .more-toggle { color: var(--ink); display: grid; place-items: center; list-style: none; width: 44px; height: 44px; padding: 0; }
.nav-more .more-toggle[aria-expanded="true"] { background: var(--accent-soft); color: var(--ink); }
.nav-more-items { position: absolute; top: calc(100% + 6px); right: 0; min-width: 11rem; max-width: calc(100vw - 32px); padding: 6px; z-index: 1; border: 1px solid var(--line); border-radius: 14px; background: var(--card); box-shadow: 0 8px 28px #0002; animation: nav-menu-appear 120ms ease-out; transform-origin: top right; }
@keyframes nav-menu-appear { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) { .nav-more-items { animation: none; } }
.nav-more-items a, .nav-more-items button { display: block; width: 100%; padding: 12px; border: 0; border-radius: 8px; background: transparent; color: var(--ink); font: inherit; text-align: left; text-decoration: none; cursor: pointer; }
.nav-more-items :is(a, button):hover { background: var(--accent-soft); }
.nav-more :focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.topbar.nav-menu-open { z-index: 21; }
`;
