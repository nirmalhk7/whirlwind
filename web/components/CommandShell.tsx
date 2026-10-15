import type { ReactNode } from "react";

export type ControlView = "overview" | "projects" | "providers" | "agents" | "inbox" | "settings" | "tasks" | "runs";
const navigation: [ControlView, string, string][] = [
  ["inbox", "Inbox", "/inbox"], ["projects", "Projects", "/projects"],
  ["runs", "Run", "/runs"],
  ["settings", "Settings", "/settings"],
];
const descriptions: Record<ControlView, string> = {
  overview: "Set the direction. Carry the work through code, checks, and review.",
  projects: "The repositories under your command. The policy that governs them.",
  runs: "Agent execution history. Inspect providers, models, thinking levels, and outcomes.",
  tasks: "Every attempt, checkpoint, and review. Progress you can inspect.",
  providers: "Connect model sources. Manage provider commands, login, and availability.",
  agents: "Choose a provider and model for each role. Set the order when quota runs out.",
  inbox: "Autonomous software engineering, in continuous motion. Bring your agents. Whirlwind handles the engineering process.",
  settings: "Configure workers, agent roles, and model providers.",
};
const iconPaths: Record<ControlView | "refresh" | "arrow" | "trash", string> = {
  overview: "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
  projects: "M3 7h6l2-3h10v16H3z M3 9h18",
  runs: "M9 4l11 8-11 8z M4 4v16",
  tasks: "M4 5h3 M10 5h10 M4 12h3 M10 12h10 M4 19h3 M10 19h10",
  providers: "M12 3v4 M12 17v4 M3 12h4 M17 12h4 M7 7h10v10H7z",
  agents: "M4 5h3 M10 5h10 M4 12h3 M10 12h10 M4 19h3 M10 19h10",
  inbox: "M4 4h16v16H4z M4 13h5l1 3h4l1-3h5",
  settings: "M4 6h16 M4 12h16 M4 18h16 M9 3v6 M16 9v6 M8 15v6",
  refresh: "M20 8a8 8 0 0 0-14-3L3 8 M3 3v5h5 M4 16a8 8 0 0 0 14 3l3-3 M21 21v-5h-5",
  arrow: "M5 12h14 M13 6l6 6-6 6",
  trash: "M3 6h18 M9 6V3h6v3 M5 6l1 15h12l1-15 M10 10v7 M14 10v7",
};
export function CommandIcon({ name }: { name: keyof typeof iconPaths }) {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={iconPaths[name]} /></svg>;
}
type ShellProps = {
  view: ControlView; loaded: boolean; error: string; busy: boolean;
  onRefresh: () => void; activeTasks: number; availableProviders: number;
  decisions: number; children: ReactNode;
};
export function CommandShell({ view, loaded, error, busy, onRefresh, activeTasks, availableProviders, decisions, children }: ShellProps) {
  const title = navigation.find(([key]) => key === view)?.[1] ?? (view === "tasks" ? "Tasks" : "Inbox");
  const connection = error ? "Offline" : loaded ? "Connected" : "Connecting";
  return (
    <div className="shell command-shell">
      <a className="skip-link" href="#command-main">Skip to content</a>
      <aside className="command-sidebar">
        <a href="/inbox" className="command-brand" aria-label="Whirlwind Inbox">
          <svg width="28" height="28" viewBox="0 0 32 32" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true"><path d="M4 10h16a5 5 0 1 0-5-5 M3 16h23a4 4 0 1 1-4 4 M7 22h7a4 4 0 1 1-4 4" /></svg>
          <span>whirlwind<span className="command-brand-underscore">_</span></span>
        </a>
        <p className="command-brand-description">ENGINEERING IN MOTION</p>
        <nav aria-label="Main navigation">
          {navigation.map(([key, label, href]) => (
            <a key={key} href={href} className={view === key ? "active" : ""} aria-current={view === key ? "page" : undefined}>
              <CommandIcon name={key} /><span>{label}</span>
              {key === "inbox" && decisions > 0 && <span className="nav-count">{decisions}</span>}
            </a>
          ))}
        </nav>
        <div className="command-sidebar-footer">
          <strong>Velocity. Control. Correctness.</strong>
          <p>Checks before review.<br />Your judgment before merge.</p>
          <span className="command-local">Local execution · Your control</span>
        </div>
      </aside>
      <main id="command-main">
        <div className="command-toolbar">
          <span>Local control center <span className="toolbar-divider" aria-hidden="true">/</span> <strong>{title}</strong></span>
          <div className="command-toolbar-actions">
            <span className={`connection-indicator connection-${connection.toLowerCase()}`} role="status"><span aria-hidden="true" />{connection}</span>
            <button className="button command-refresh" disabled={busy} onClick={onRefresh}><CommandIcon name="refresh" /><span>Refresh</span></button>
          </div>
        </div>
        <header className={`command-heading ${view === "inbox" ? "command-hero" : ""}`}>
          <div><h1>{view === "inbox" ? "Ideas in. Verified software out." : title}</h1><p className="sub">{descriptions[view]}</p></div>

        </header>
        {view === "inbox" && <ul className="command-brand-pillars" aria-label="Whirlwind capabilities"><li>Agent-agnostic</li><li>ACP-native</li><li>Durable execution</li><li>Evidence-driven</li></ul>}
        {view === "inbox" && <div className="command-readiness" aria-label="Inbox status">
          <a href="/tasks"><span className="readiness-value">{loaded && !error ? activeTasks : "—"}</span> active tasks <CommandIcon name="arrow" /></a>
          <a href="/settings#providers"><span className="readiness-value">{loaded && !error ? availableProviders : "—"}</span> available providers <CommandIcon name="arrow" /></a>
          <a href="/inbox"><span className="readiness-value">{loaded && !error ? decisions : "—"}</span> Inbox items <CommandIcon name="arrow" /></a>
          <p>Acceptance standard <strong>9/10 + passing checks</strong></p>
        </div>}
        <div className={`command-content command-view-${view}`}>{children}</div>
        <footer className="command-main-footer"><span>WHIRLWIND</span><p>Plan with intent. Execute with discipline. Finish with evidence.</p></footer>
      </main>
    </div>
  );
}
