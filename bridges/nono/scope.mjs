import {isAbsolute, join, normalize} from 'node:path';

const escapeRegex = value => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// Seatbelt evaluates canonical paths, so a link cannot redirect an allowed
// lexical name into a different writable subtree.
export function codingRules(profile, cwd) {
  if (profile.read_only) return [];
  return (profile.allowed_paths ?? []).map(pattern => {
    if (pattern === '.' || normalize(pattern) !== pattern || isAbsolute(pattern) ||
        pattern.split('/').includes('..') || /[\\\[\]{}\x00\r\n]/.test(pattern)) {
      throw new Error('Unsupported or unbounded coding scope');
    }
    if (pattern === '**') return `(allow file-write* (subpath ${JSON.stringify(cwd)}))`;
    if (pattern.endsWith('/**')) {
      const directory = join(cwd, pattern.slice(0, -3));
      return `(allow file-write* (subpath ${JSON.stringify(directory)}))`;
    }
    if (!/[*?]/.test(pattern)) return `(allow file-write* (literal ${JSON.stringify(join(cwd, pattern))}))`;
    const glob = pattern.split(/(\*+|\?)/).map(part => {
      if (part.startsWith('*')) return '[^/]*';
      if (part === '?') return '[^/]';
      return escapeRegex(part);
    }).join('');
    return `(allow file-write* (regex ${JSON.stringify(`^${escapeRegex(cwd)}/${glob}$`)}))`;
  });
}
