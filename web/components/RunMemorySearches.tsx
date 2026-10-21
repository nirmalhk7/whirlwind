"use client";

type MemorySearch = { query: string; bytes: number; reason: string; lessons: { id: string; version: number }[] };

export function RunMemorySearches({ searches = [], calls = 0, bytes = 0 }: { searches?: MemorySearch[]; calls?: number; bytes?: number }) {
  return <div>
    <p className="muted">On-demand memory: {calls} of 2 searches · {bytes} of 4096 bytes used</p>
    {searches.map((search, index) => <details key={index}>
      <summary>Memory search: {search.query}</summary>
      <p className="muted">{search.reason} · {search.bytes} bytes</p>
      <ul>{search.lessons.map(lesson => <li key={`${lesson.id}:${lesson.version}`}>Lesson {lesson.id} · v{lesson.version}</li>)}</ul>
    </details>)}
  </div>;
}
