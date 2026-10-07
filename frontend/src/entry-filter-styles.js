// Ship the shared controls and their layout together, including embedded stories.
export const entryFilterStyles = `
.entry-filters.entry-filters {
  display: block;
  container-type: inline-size;
  box-sizing: border-box;
  min-width: 0;
  margin: 16px 0;
  padding: 0 16px;
  border: 1px solid var(--line, #e3ddd4);
  border-radius: 12px;
  background: var(--card, #fffdfa);
  color: var(--ink, #39352f);
}
/* The persona bar already supplies the gap above timeline controls. */
.who-bar + div .entry-filters { margin-top: 0; }
.sync-status:has(.hint:empty):has(button[hidden]) { display: none; }
.entry-filters > summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 44px;
  font-size: 1rem;
  cursor: pointer;
  list-style: none;
}
.entry-filters > summary::-webkit-details-marker { display: none; }
.entry-filters > summary::before { content: "›"; font-size: 1.4375rem; transition: transform .15s; }
.entry-filters[open] > summary::before { transform: rotate(90deg); }
.entry-filters .entry-filter-status { margin-left: auto; color: var(--accent, #a46730); font-size: 0.9375rem; }
.entry-filters .entry-filter-status:empty { display: none; }
.entry-filters .entry-filter-body { padding-bottom: 16px; }
.entry-filters .entry-filters-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0 12px;
  margin-bottom: 8px;
}
.entry-filters .entry-filter-favorite {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  font-size: 1rem;
  cursor: pointer;
}
.entry-filters input[type="checkbox"] {
  flex: 0 0 auto;
  width: 18px;
  height: 18px;
  margin: 0;
  accent-color: var(--accent, #a46730);
}
.entry-filters .entry-filter-clear {
  min-height: 40px;
  margin-left: auto;
  padding: 4px 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--muted, #807a70);
  font: inherit;
  font-size: 0.9375rem;
  cursor: pointer;
}
.entry-filters .entry-filter-clear:hover { color: var(--ink, #39352f); text-decoration: underline; }
.entry-filters .entry-filter-dates {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.entry-filters .entry-filter-dates > label {
  display: grid;
  gap: 6px;
  min-width: 0;
  color: var(--muted, #807a70);
  font-size: 0.9375rem;
}
.entry-filters input[type="date"] {
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  height: 44px;
  padding: 8px;
  border: 1px solid var(--line, #e3ddd4);
  border-radius: 8px;
  background: var(--bg, #faf8f4);
  color: var(--ink, #39352f);
  font: inherit;
  font-size: 16px;
  /* iOS Safari draws its own centred field; keep it the same as elsewhere. */
  -webkit-appearance: none;
  appearance: none;
  display: block;
  line-height: 26px;
  text-align: left;
}
.entry-filters input[type="date"]::-webkit-date-and-time-value { margin: 0; text-align: left; }
.entry-filters input[type="date"]::-webkit-datetime-edit { padding: 0; }
.entry-filters .entry-filter-date { position: relative; display: block; min-width: 0; max-width: 100%; }
/* Only iOS leaves an empty date field blank; other browsers show their own hint. */
@supports (-webkit-touch-callout: none) {
  .entry-filters .entry-filter-date[data-empty]::after {
    content: attr(data-hint);
    position: absolute;
    left: 9px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--muted, #807a70);
    font-size: 16px;
    pointer-events: none;
  }
}
.entry-filters :is(input, button, summary):focus-visible { outline: 2px solid var(--accent, #a46730); outline-offset: 2px; }
.entry-filters [role="alert"] { display: block; margin-top: 8px; font-size: 1rem; }
.entry-filters [role="alert"]:empty { display: none; }
/* A phone's filter is about 310-330px wide, narrower than two iOS date fields
   side by side (Safari will not shrink one below its date text), so they
   stack there rather than run into each other. */
@container (max-width: 360px) {
  .entry-filters .entry-filter-dates { grid-template-columns: minmax(0, 1fr); }
}
`;
