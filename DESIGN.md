---
name: Whirlwind
description: Engineering in motion for a daily local working control center.
colors:
  velocity-blue: "#38BDF8"
  energy-violet: "#A78BFA"
  signal-amber: "#FBBF24"
  ion-teal: "#5EEAD4"
  ion-teal-hover: "#99F6E4"
  ion-teal-pressed: "#2DD4BF"
  midnight: "#0B1220"
  graphite: "#101C2D"
  raised-graphite: "#162438"
  sidebar-ground: "#0E1726"
  cloud: "#F8FAFC"
  quiet-cloud: "#A8B8CC"
  ruled-edge: "#304158"
  teal-ink: "#071B1B"
  field-ground: "#0B1525"
  control-edge: "#51657E"
  control-hover: "#1B3046"
  control-pressed: "#244057"
  field-edge: "#51657E"
  field-placeholder: "#A0B1C8"
  ready-teal: "#5EEAD4"
  ready-ground: "#102E2C"
  ready-edge: "#28655B"
  failure-coral: "#FDA4AF"
  failure-ground: "#351C28"
  failure-edge: "#8B485F"
  inactive-ground: "#1B293B"
  inactive-edge: "#3F526C"
  pending-ground: "#342C17"
  pending-edge: "#806A2B"
typography:
  display:
    fontFamily: "\"Barlow Condensed\", sans-serif"
    fontSize: "72px"
    fontWeight: 700
    lineHeight: 0.95
    letterSpacing: "-.015em"
  wordmark:
    fontFamily: "\"Barlow Condensed\", sans-serif"
    fontSize: "27px"
    fontWeight: 700
    lineHeight: 0.95
    letterSpacing: ".01em"
  headline:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, sans-serif"
    fontSize: "40px"
    fontWeight: 600
    lineHeight: 1.1
    letterSpacing: "-.03em"
  empty-title:
    fontFamily: "\"Barlow Condensed\", sans-serif"
    fontSize: "36px"
    fontWeight: 700
    lineHeight: 1.05
  section-title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "17px"
    fontWeight: 600
    letterSpacing: "-.01em"
  record-title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.5
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "15px"
    lineHeight: 1.55
  button-label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "13px"
    fontWeight: 600
    lineHeight: 1.4
  status-label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif"
    fontSize: "11px"
    fontWeight: 500
    letterSpacing: ".015em"
rounded:
  badge: "4px"
  control: "6px"
  toast: "8px"
  container: "12px"
spacing:
  micro: "4px"
  compact: "8px"
  control-gap: "9px"
  action-gap: "10px"
  small: "12px"
  base: "16px"
  row: "20px"
  section: "24px"
  panel: "26px"
  column: "28px"
  room: "32px"
  gutter: "48px"
components:
  button-primary:
    backgroundColor: "{colors.ion-teal}"
    textColor: "{colors.teal-ink}"
    typography: "{typography.button-label}"
    rounded: "{rounded.control}"
    padding: "10px 16px"
    height: "42px"
  button-primary-hover:
    backgroundColor: "{colors.ion-teal-hover}"
  button-primary-active:
    backgroundColor: "{colors.ion-teal-pressed}"
  button-secondary:
    backgroundColor: "transparent"
    textColor: "{colors.cloud}"
    typography: "{typography.button-label}"
    rounded: "{rounded.control}"
    padding: "10px 16px"
    height: "42px"
  button-disabled:
    backgroundColor: "{colors.inactive-ground}"
    textColor: "{colors.quiet-cloud}"
  input:
    backgroundColor: "{colors.field-ground}"
    textColor: "{colors.cloud}"
    rounded: "{rounded.control}"
    padding: "12px 14px"
    height: "44px"
  nav-item:
    textColor: "{colors.quiet-cloud}"
    rounded: "{rounded.control}"
    padding: "11px 13px"
    height: "46px"
  nav-item-active:
    backgroundColor: "{colors.ion-teal}"
    textColor: "{colors.teal-ink}"
  status:
    backgroundColor: "{colors.control-hover}"
    textColor: "{colors.ion-teal}"
    typography: "{typography.status-label}"
    rounded: "{rounded.badge}"
    padding: "4px 8px"
  status-ready:
    backgroundColor: "{colors.ready-ground}"
    textColor: "{colors.ready-teal}"
  status-pending:
    backgroundColor: "{colors.pending-ground}"
    textColor: "{colors.ion-teal-hover}"
  panel:
    backgroundColor: "{colors.graphite}"
    textColor: "{colors.cloud}"
    rounded: "{rounded.container}"
    padding: "{spacing.panel}"
  wordmark:
    textColor: "{colors.cloud}"
    typography: "{typography.wordmark}"
---

# Design System: Whirlwind

## Overview

**Creative North Star: "Engineering in motion"**

Engineering in motion turns strength into composure: midnight grounds, cloud white text, and ion teal hold a clear operator hierarchy. The dark scene supports long local working sessions. Condensed identity lettering supplies force while familiar operational type keeps forms, evidence, and decisions readable.

Flight-readiness and checklist dispatch shape the recurring material: ruled records, predictable status placement, and clear space between controls. Flat surfaces and restrained corners keep attention on state and the next action. The implementation is code-first; authored geometric SVG supplies its mark and compact icons, with no shipping raster imagery.

**Key Characteristics:**

- Dark midnight and graphite grounds with cloud white text.
- Teal marks action, selection, and directional links.
- Barlow Condensed identity paired with familiar system sans operations.
- Ruled records, fixed status positions, and visible operator actions.
- Flat containers, compact geometric SVG, and restrained state transitions.

## Colors

Ion teal and cloud white emerge from midnight and graphite; semantic teal and coral retain clarity without turning the interface into a glowing console. The frontmatter owns exact values; these names describe their application.

### Primary

- **Ion Teal:** Primary actions, active navigation, directional links, and the geometric mark.
- **Ion Teal Hover:** Primary hover and focus visibility; also pending decisions.
- **Ion Teal Pressed:** Pressed primary actions.

### Secondary

- **Ready Teal:** Available providers, completed stages, connected state, and success notifications, supported by Ready Ground and Ready Edge.
- **Failure Coral:** Offline state, errors, and failure notifications, supported by Failure Ground and Failure Edge.

### Neutral

- **Midnight:** Page ground.
- **Graphite / Raised Graphite:** Enclosed panels and raised feedback surfaces.
- **Sidebar Ground:** Persistent navigation region.
- **Cloud / Quiet Cloud:** Main reading text and supporting metadata.
- **Ruled Edge:** Structural dividers and container boundaries.
- **Teal Ink:** Dark text on teal selection and action surfaces.
- **Field Ground / Field Edge / Field Placeholder:** Readable entry surfaces and secondary entry cues.
- **Control Edge / Control Hover / Control Pressed:** Outlined controls and their surface response.
- **Inactive Ground / Inactive Edge:** Disabled controls; muted labels retain legibility.
- **Pending Ground / Pending Edge:** Questions and approval states beneath teal text.

**The Ion Teal Rule.** Use teal for action, selection, and directional links; reserve teal and coral for truthful readiness and failure.

## Typography

**Display Font:** Self-hosted Barlow Condensed Bold, with sans-serif fallback. The bundled font carries its OFL license.

**Body Font:** Platform system sans using the frontmatter body stack. Operational page headlines use the existing ui-sans-serif/system-ui stack.

**Character:** Narrow, emphatic identity lettering sits above a calm operational reading voice. Sentence case and familiar labels keep working controls clear.

### Hierarchy

- **Display:** Inbox identity; desktop token reduces to 72px at the intermediate breakpoint and 62px on small screens.
- **Wordmark:** WHIRLWIND identity; desktop token reduces to 25px at the intermediate breakpoint and becomes 28px on small screens.
- **Headline:** Operational page titles; reduces to 32px on small screens.
- **Empty Title:** Condensed empty-state prompts; the task prompt and small-screen variants use 30px.
- **Section Title / Record Title:** List grouping and work records, respectively.
- **Body:** General reading. Supporting page copy uses a 65ch maximum; record copy uses 70ch. Inbox supporting copy narrows to 45ch, then 34ch on small screens.
- **Button Label / Status Label:** Compact functional labels. Readiness counts use tabular numerals.

**The Two Voices Rule.** Use Barlow Condensed for identity and prominent empty-state language; use system sans for operational headings, controls, and records.

## Layout

The desktop shell uses a sticky, viewport-height sidebar (220px) and a flexible main column capped at 1540px. Main padding is 24px 48px 28px. Inbox is the default screen, carrying the Whirlwind identity above decision records. Other views retain the shared shell and compact headings. Workspace navigation is removed.

At 1150px and below, the sidebar narrows to 190px, main horizontal padding becomes 32px,. At 700px and below, the sidebar becomes a top region, navigation forms a three-column, two-row grid, and main padding becomes 20px 22px 24px. The toolbar wraps and readiness links stack into rows.

Spacing is functional rather than a strict arithmetic scale: compact control gaps, generous record separation, and broad page margins. Forms use a 20px vertical gap with 8px label-to-field separation and a 680px maximum width. Panels use the frontmatter panel padding, reduced to 20px on small screens.

## Elevation & Depth

Most depth comes from tonal grounds and one-pixel rules. Buttons, navigation, cards, and records have no resting shadows or glow. Toast feedback is the sole active shared floating layer; its shadow is recorded in the sidecar. Do not propagate superseded dialog or card shadows from inactive CSS into new control-center surfaces.

**The Ruled Record Rule.** Separate repeated work records with rules and spacing; reserve enclosed panels for forms and focused details.

## Shapes

Containers use restrained rounded corners, controls a tighter radius, and badges the smallest radius in the frontmatter. Connection dots alone use circles; navigation counts use the badge radius. Record lists remain open, divided by straight rules. Authored SVG icons use compact geometric paths, rounded stroke caps and joins, and a consistent 1.5px stroke; ordinary navigation icons occupy 20px, reducing to 16px on small screens.

## Components

### Buttons

Confident, compact controls with visible state feedback. Primary buttons use teal and dark Teal Ink; secondary buttons stay transparent with Cloud text and Control Edge. Both use the control radius, 10px 16px padding, a 42px minimum height, and a 9px icon gap. Hover, pressed, and disabled styles come from the token variants. Refresh is a compact secondary variant with a 36px minimum height. State transitions last .18s with ease easing; pressed controls do not move. Keyboard focus uses a two-pixel amber outline with four-pixel offset.

### Status Labels

Small rectangular badges keep workflow state in a predictable record position. Default state uses teal on Control Hover with a visible border. Ready state uses teal, pending questions or approvals use lighter teal, and paused or unavailable state uses Quiet Cloud. Text always accompanies color. Connection state similarly pairs a small dot with a visible label.

### Cards / Containers

Flat Graphite panels use a Ruled Edge border, the container radius, and generous internal padding. Task details receive a slightly lighter local ground. Repeated work records use open ruled rows instead of nested cards.

### Inputs / Fields

Field Ground, Cloud entry text, Field Edge, the control radius, 12px 14px padding, and a 44px minimum height provide a quiet working surface. Hover lightens the border; focus shifts it to teal and retains the global outline. Placeholder text uses its dedicated muted tone. Textareas resize vertically and keep evidence readable.

### Folder Selector

Repository registration includes a Choose folder action beside the path field. A native dialog browses directories on the local server, with Home, Up one level, a location field, and Use this folder. Its container reuses the panel radius and ruled edges; the directory list scrolls while selection controls stay visible. Cancel leaves the field unchanged. Small screens stack the repository field and chooser button.

### Navigation

Desktop navigation uses a vertical stack of icon-and-label links with 46px minimum height. Active pages use solid teal with dark ink and stronger weight; hover uses a darker tonal panel. Current-page semantics remain explicit. Small-screen links use 42px minimum height in the three-column, two-row grid. Inbox counts remain text, with tabular numerals.

Agents sits beside Providers as a distinct operational destination. Tasks is absent from the menu; Inbox links into the active task view. Preserve the shared teal selection, cloud labels, midnight grounds, and geometric icon language across both configuration surfaces.

### Agent Editor

Role configuration uses open ruled sections, with a provider selector and model field paired horizontally on desktop and stacked on small screens. Native selectors retain dark field styling. Loading, test replies, and errors stay beside the provider/model pair that produced them. Ordered fallback rows expose reorder and remove controls, keeping execution order readable without nested cards. Saving remains a clear page action with visible feedback.

### Wordmark and Readiness Records

The WHIRLWIND wordmark combines the self-hosted condensed identity face with an authored wind mark. Ruled readiness links pair tabular counts, readable labels, and compact arrow SVG. Missing connection data stays visibly unknown. Directional link arrows move four pixels on hover using a .2s decelerating curve; reduced-motion preference removes that travel and minimizes all transitions and animation.

## Do's and Don'ts

### Do:

- **Do** keep status labels beside their records and pair color with readable state text.
- **Do** use the two type voices consistently: condensed identity, system sans operations.
- **Do** preserve the two-pixel amber focus outline and visible keyboard path.
- **Do** use ruled lists for repeated work and enclosed panels for forms or focused detail.
- **Do** provide a concrete next action in empty states.
- **Do** keep navigation available as a three-column, two-row grid on small screens.

### Don't:

- **Don't** replace truthful unavailable, loading, or failed state with decorative success.
- **Don't** apply glow, gradients, or promotional imagery to the grounded operational surface.
- **Don't** spread display lettering into form fields, long evidence, or routine control labels.
- **Don't** use color as the sole status signal.
- **Don't** inherit the superseded cyan palette from inactive selectors in globals.css.

## Whirlwind brand integration

Wordmark: `whirlwind_`, with a teal underscore and authored geometric wind mark.
Signature: ENGINEERING IN MOTION. Inbox headline: Ideas in. Verified software out.
Capabilities: Agent-agnostic, ACP-native, Durable execution, Evidence-driven.
Midnight `#0B1220`, ion teal `#5EEAD4`, velocity blue `#38BDF8`, energy violet
`#A78BFA`, cloud white `#F8FAFC`, and signal amber `#FBBF24` define the palette.
Keep current Inbox-first navigation, task links, role configuration, and project
folder selection. Inbox headline scales from 44px to 72px, capped at 15ch.
The mobile signature sits below the wordmark rather than beside it.


## Idea selection, Run history, and scheduling

Idea descriptions use 35-word previews with independently operable full-text expansion. Whole-card native selection retains amber keyboard focus and accessible Liked state, with no visible selection button. A downward chevron expands historical descriptions and points upward when expanded. Pending and selected ideas use solid Velocity Blue cyan `#38BDF8` with dark `#071B1B` text; pending states prevent duplicate actions and failures restore unselected styling.

Run joins sidebar as read-only execution history. Ruled rows show role, provider, model, thinking level, transport, project/task, timing, and truthful outcome; unknown historical fields stay explicit. Desktop metadata uses four columns, medium widths two, mobile one.

Agents pairs provider, model, and ACP-advertised Thinking level in three desktop columns and stacks them on mobile. Settings adds automatic daily ideation toggle, local time, and explicit timezone using incumbent fields and flat ruled grouping.

## Command feedback motion

Motion reinforces operator decisions inside fixed controls. Inbox entries never animate their position, and routine navigation has no entrance sequence. Idea cards retain immediate cyan intent feedback; an in-place ellipsis marks an outstanding request, and the Font Awesome check appears only after the selection is saved. Its 240ms clipped reveal uses `cubic-bezier(.16, 1, .3, 1)`; supporting opacity feedback lasts 120ms. Icon space is reserved in every card so state changes cannot shift its content.

Processing receives one 600ms outline pulse at run start, then remains still. Stop/Resume labels acknowledge an actual state change with a 120ms opacity transition. Selected-idea counts acknowledge a changed value with a 500ms color transition to teal. No feedback effect loops or delays command execution; pressed controls keep their bounds.

Reduced motion replaces the clipped check reveal with an 80ms opacity transition, removes the Processing pulse and label/count animations, and retains explicit state text, static cyan selection, and the confirmed check.

## YAML indentation guides

Startup snapshots and the native file editor use 1px Ruled Edge indentation guides inside leading whitespace. Guides follow the document's indentation spacing, with two-column tabs and shared 13px monospace / 24px row metrics. The editor's decorative guide layer tracks both scroll axes and resizing while the native textarea owns all text, selection, and caret behavior. Guides are hidden from assistive technology and never alter copied or saved YAML.

## Provider status in Settings

Settings places a compact provider list above the startup YAML. Plain divided rows show provider name, text status, check detail, transport, and local check time. Teal indicates working protocol connectivity; rose indicates failure; neutral text indicates unavailable or paused checks. Status never depends on color alone. Polling preserves YAML editor state and uses the configured interval; failed refreshes explicitly mark previous results outdated.

## Project activity controls

Each project row offers Pause activity or Resume activity beside its policy and removal controls. Paused projects show a neutral Activity paused badge alongside their policy readiness. Pause cancels that project's active agents and blocks task execution and ideation until resumed; saved checkpoints and individual task pauses remain. Controls use the existing button, disabled, focus, and toast patterns. State changes appear after the versioned request succeeds and survive refresh. Global runner stop remains independent.
