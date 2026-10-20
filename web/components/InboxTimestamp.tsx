export function InboxTimestamp({ value }: { value?: string }) {
  if (!value || !Number.isFinite(Date.parse(value))) return <span className="inbox-timestamp">Timestamp unavailable</span>;
  const label = new Date(value).toLocaleString(undefined, {
    year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit", timeZoneName: "short",
  });
  return <time className="inbox-timestamp" dateTime={value} title={`Last updated: ${label}`}>Updated {label}</time>;
}
