export function candidatePreview(path: string) {
    const protectedCandidate = path.includes("protected-candidate");
    return {
        candidate_digest: protectedCandidate ? "digest-protected" : "digest-dirty",
        destination_commit: "base-commit",
        changed_files: [protectedCandidate ? "secrets/token.txt" : "src/file.py"],
        protected_files: protectedCandidate ? ["secrets/token.txt"] : [],
        dirty_destination: !protectedCandidate,
    };
}
