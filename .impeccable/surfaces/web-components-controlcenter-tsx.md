---
version: 1
slug: "web-components-controlcenter-tsx"
primary_target: "web/components/ControlCenter.tsx"
related_targets: ["web/components/CommandShell.tsx","web/app/command.css","web/app/layout.tsx"]
---

# Control center

Scope: active working routes and shared shell. Mode: Operate.
User confirmed daily working control center, code-first, indomitable personality,
then explicitly directed immediate execution: “Go go go”. No image comp round.

## Direction contract

THESIS: Engineering in motion. Powerful identity above a disciplined operator
surface; replace the generic glowing cyan console with readable execution records.

OWN-WORLD: Midnight and graphite grounds, cloud white text, ion teal reserved
for action and selection, condensed Barlow lettering for identity, familiar system
sans for controls. Ruled task rows, durable status labels, and broad clear margins.

STORY: Choose a direction, follow implementation, inspect evidence, decide.
Actual counts and connection state stay truthful. Confidence does not conceal
failed checks, unavailable agents, or human approval.

FIRST VIEWPORT: Persistent left navigation with WHIRLWIND wordmark and wind mark.
Slim connection/refresh strip. Workspace carries Whirlwind headline with concise
execution copy and agent capabilities. Project directions and tasks form
two distinct working columns with explicit empty actions; real content replaces
emptiness. Other routes use compact page headings and the same control language.

FORM: Grounded candidate 7, flight readiness/checklist dispatch. Seed 4f1132c6.
Other candidates: launch console, naval bridge, chess scorebook, Swiss rail
dispatch, pit wall, architectural section book. Challenger verdicts: streaming
wall declined (metadata stays visible); airport signs competitive on wayfinding,
declined for operator identification (next action clarity retained); lexicon
competitive on reading, declined for operational familiarity (measure retained);
labanotation and grid specimen declined (alignment retained); split-flap board
competitive for dispatch, declined for readable asynchronous updates (fixed
state placement retained). User's go instruction resolves direct implementation.
Raise from signage: primary operator action is visible at each empty state.
Raise from split-flap: status stays in predictable position as data changes.
Raise from lexicon: quiet prose stays at a bounded reading measure.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Inbox and folder selection update

Inbox is now the default route; Workspace navigation is removed. Inbox inherits the responsive Whirlwind identity, capabilities, and live readiness strip. Projects retains directions and registration. Registration adds a local directory browser in a native dialog, using the existing graphite/teal fields and controls. Choosing a directory fills its absolute path; cancellation preserves the previous value. No files are uploaded.
