type FailureScenario = {
    id: string;
    summary: string;
    revision: string;
    fingerprint: string;
    phase: string;
    detail: string;
    artifact: string;
    reply: string;
};
export function repeatedFailureScenario(input: FailureScenario) {
    return {
        run: {
            id: input.id, type: "run", project: "smoke-project", status: "needs_human", summary: input.summary, updated_at: input.revision,
            record: { failure_fingerprint: input.fingerprint, repeated_failures: 2 },
            evidence: {
                run: { status: "needs_human", failure_fingerprint: input.fingerprint, repeated_failures: 2 },
                events: [{ phase: input.phase, message: input.detail }], artifacts: [input.artifact],
            },
        },
        fingerprint: input.fingerprint, detail: input.detail, artifact: input.artifact, reply: input.reply,
    };
}
