# Whirlwind UI audit

Audit date: October 14, 2026. Scope: integrated Whirlwind interface on `main`, following the Impeccable audit workflow. This report records issues; it does not apply the recommended fixes. Findings describe the historical audit snapshot; source files have since been reorganized. This cleanup retains the findings without revalidating or marking them resolved.

## Implementation integrity verdict

**Pass, with follow-up findings.** The running interface expresses Whirlwind through its lower-case underscore wordmark, wind mark, midnight canvas, teal actions, blue/violet capabilities, and amber keyboard focus. Current Inbox-first navigation, provider setup, four agent roles, folder selection, and task evidence remain intact. Operational states use readable labels alongside color.

The bundled detector returned `[]` for `web/`. That result is a mechanical scan, not proof of accessibility or correctness. Runtime inspection found the settings issue below despite the clean detector result.

## Audit health

| Dimension | Score | Key finding |
| --- | --- | --- |
| Accessibility | 3/4 | Labels and keyboard flow work; server errors need durable form feedback |
| Performance | 3/4 | Production build is lean enough for tested state; full TTF and collection refreshes leave room for optimization |
| Responsive design | 3/4 | No page overflow at tested widths; touch targets are often below 44px |
| Theming | 2/4 | Brand tokens exist, but component CSS repeats literal colors |
| Implementation integrity | 3/4 | Coherent identity; refreshed settings can overwrite unseen values |
| **Total** | **14/20** | **Good — address weak dimensions** |

Seven findings: **0 P0, 1 P1, 4 P2, 2 P3**. Scores are a bounded engineering assessment, not WCAG certification.

## Evidence and coverage

- Production Next.js build passed, including TypeScript checks.
- `go test -race ./...` and `go vet ./...` passed for the integrated implementation.
- All **12 browser tests passed**, including onboarding, folder selection, questions, permissions/security boundaries, Inbox ordering, idea selection, and verified PR delivery using local fixture agents/GitHub commands.
- Runtime sweep: `/inbox`, `/projects`, `/providers`, `/agents`, `/settings`, `/tasks`, `/memory`, `/projects/detail`, and `/agents/detail` at **1440, 768, 390, and 320 CSS pixels**: 36 route/viewport combinations. The final three routes redirect to supported destinations. `/` redirect to Inbox is covered by the browser suite; legacy `/records/[id]` redirects to `/` by source inspection.
- Captured additional offline, populated Inbox, permission/question, and task-detail states. Populated scenarios are explicitly synthetic fixtures, not operator data.
- Measured visible text colors against ancestor backgrounds: no below-threshold text contrast in the base route sweep. All visible form fields in that sweep had labels. Disabled fields, placeholders, hover states, and every possible provider response are not covered by that contrast result.
- No document-level horizontal overflow in the sweep or populated desktop/mobile states.
- Folder dialog stayed within the narrow viewport, contained keyboard focus, closed with Escape, and restored focus to Choose folder. Skip link showed amber focus and placed the next Tab inside the main content.
- Five shared production JS chunks total approximately **135 KiB when gzip-compressed locally**; this is a file estimate, not measured network transfer. Warm-cache transfer metrics were zero and were not treated as a cold-load benchmark. No high-volume/task-history load test or non-Chromium screen-reader test was performed.

The measurements and captures were generated audit artifacts and are not retained in version control. The observations above record that historical run.

## Findings

### [P1] Refreshed settings retain stale inputs but submit the latest version

**Location:** [ControlCenter.tsx](../web/components/ControlCenter.tsx), [ControlCenter.tsx](../web/components/ControlCenter.tsx). **Category:** Implementation integrity.

`refresh()` replaces the settings record and version. The inputs use `defaultValue`, so an already mounted form keeps its previous values. Submission uses the newly received version alongside those stale values.

Reproduction with intercepted settings responses: load global slots `4`, version `1`; change server response to slots `8`, version `2`; click Refresh. The input still displays `4`. Save submits `global_slots: 4` with `If-Match: 2`. This can overwrite a newer setting without showing it to the operator.

**Recommendation:** Track draft values and their source version together. Refresh untouched drafts; preserve edited drafts with an explicit conflict notice. Never pair an old draft with an unseen new version. Add a regression check for this exact sequence. **Suggested command:** `$impeccable harden`. No WCAG claim; this is a reproduced data-consistency defect.

### [P2] Frequently used mobile targets miss the 44px guideline

**Location:** [command.css](../web/app/command.css), [command.css](../web/app/command.css), [command.css](../web/app/command.css). **Category:** Responsive design/accessibility.

Measured mobile navigation and main buttons: **42px** high; Refresh: **36px**; readiness links: **38px**; Manage projects: **18.6px**. Small vertical targets make tapping harder, especially for operators with limited dexterity.

**Recommendation:** Use at least 44px interactive height for standalone actions and navigation, retaining compact visual text inside larger hit areas. **Suggested command:** `$impeccable adapt`.

This is a usability/enhanced-target finding, not an automatic AA violation: [WCAG 2.5.5 uses 44px at AAA](https://www.w3.org/WAI/WCAG22/Understanding/target-size-enhanced.html); [WCAG 2.5.8 uses 24px with spacing and other exceptions at AA](https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html). The isolated Manage projects link needs its spacing evaluated, rather than being declared a failure from its height alone.

### [P2] Inbox places new ideas ahead of work waiting for decisions

**Location:** [ControlCenter.tsx](../web/components/ControlCenter.tsx), [CommandShell.tsx](../web/components/CommandShell.tsx). **Category:** Implementation integrity/interaction hierarchy.

Project ideation and its four direction cards precede Messages. In the populated fixture, the first pending answer field starts at **1496px** on desktop and **2435px** on mobile. Questions and permission decisions that can unblock existing work require substantial scrolling past opportunities for new work.

**Recommendation:** Put pending decisions before ideation, or provide a visible direct jump to pending messages. Keep brand identity compact in the daily operating scene. **Suggested command:** `$impeccable layout`. This is a measured ordering problem plus product-specific visual judgment, not a detector finding or WCAG violation.

### [P2] Server validation errors disappear without durable field feedback

**Location:** [ControlCenter.tsx](../web/components/ControlCenter.tsx), [ControlCenter.tsx](../web/components/ControlCenter.tsx), [ToastProvider.tsx](../web/components/ToastProvider.tsx). **Category:** Accessibility.

Generic mutation errors are sent to a toast that disappears after **4.5 seconds**. Settings has no persistent inline error or field association; provider setup uses the same mutation helper. Native required/min/max validation and project-registration inline errors already help, but server-side validation/conflict failures can leave no durable explanation after the toast closes.

**Recommendation:** Retain a form-level error until corrected, associate field-specific errors with `aria-describedby`/`aria-invalid`, and preserve entered values. Keep toast as supplementary feedback. **Suggested command:** `$impeccable harden`. A complete WCAG error-identification determination requires exercising individual server errors with assistive technology; no blanket violation is asserted here.

### [P2] Supporting component colors are duplicated rather than tokenized

**Location:** [command.css](../web/app/command.css), [command.css](../web/app/command.css). **Category:** Theming.

The command stylesheet contains **60 hex-color occurrences**, including its root tokens. Navigation, pressed/disabled controls, badges, fields, code surfaces, and toast states repeat supporting colors directly. Primary brand tokens are present and current dark-theme contrast is sound, but changing a root token will not consistently update these components.

**Recommendation:** Extract named semantic tokens for control, field, success, pending, error, and raised surfaces; map component selectors to them. Retain the supplied palette and dark-only operating context. **Suggested command:** `$impeccable extract`. No missing light theme is reported; a light theme was not requested.

### [P3] Reduced motion uses a global near-zero duration override

**Location:** [globals.css](../web/app/globals.css). **Category:** Accessibility.

All animation and transition durations become `.01ms`. Current state labels remain visible, and command-link movement has a dedicated reduced-motion override, so this is not a task blocker. The global shortcut still makes future motion/state distinctions difficult to maintain deliberately.

**Recommendation:** Use explicit reduced-motion treatments for toast arrival, links, and any future progress animation; preserve immediate state changes and readable feedback. **Suggested command:** `$impeccable animate`.

### [P3] Identity font ships as a full TTF

**Location:** [command.css](../web/app/command.css), `web/public/fonts/BarlowCondensed-Bold.ttf`. **Category:** Performance.

The self-hosted bold font is approximately **107 KiB**. `font-display: swap` already prevents invisible text, but the full TTF adds avoidable cold-load bytes for a single weight.

**Recommendation:** Serve a WOFF2 version and, if supported-language coverage permits, a carefully scoped subset. Preserve the font license and required glyphs. **Suggested command:** `$impeccable optimize`. No observed loading failure or frame-rate problem is claimed.

## Patterns and strengths

The main systemic gaps are draft/version synchronization, transient server-error feedback, mobile target sizing, and repeated literal color values. Those matter more than adding decorative polish.

Keep the existing native labelled dialog, Escape/focus restoration, skip link, amber focus outline, explicit current-page state, meaningful empty states, provider availability labels, versioned mutation requests, and reduced-motion awareness. Four ideas lay out in four columns on desktop and one on mobile without overflow. Long commands and task evidence wrap within their surfaces. The rebrand retained the existing operational controls rather than removing functionality.

## Recommended order

1. **[P1] `$impeccable harden`:** Couple settings draft/version and add the reproduced regression check.
2. **[P2] `$impeccable harden`:** Persist and associate server-side form errors.
3. **[P2] `$impeccable adapt`:** Increase mobile action and navigation hit areas.
4. **[P2] `$impeccable layout`:** Put pending Inbox decisions before ideation or add a direct jump.
5. **[P2] `$impeccable extract`:** Centralize supporting semantic colors.
6. **[P3] `$impeccable animate`:** Replace blanket reduced-motion timing with explicit treatments.
7. **[P3] `$impeccable optimize`:** Reduce font transfer cost while preserving glyph coverage.
8. **`$impeccable polish`:** Final bounded consistency pass after correctness and accessibility work.

You can ask me to run these one at a time, all at once, or in any order you prefer. Re-run `$impeccable audit` after fixes to compare evidence and scores.
