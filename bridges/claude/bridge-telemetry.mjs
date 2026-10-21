export function optionalModel(model) {
  return model ? { resolved_model: model } : {};
}

export function attemptUsage(message, tokens, cost) {
  const counts = Object.fromEntries(["input_tokens", "output_tokens", "cache_read_input_tokens"].map((key) => [key, message.usage[key] ?? null]));
  return { input_tokens: counts.input_tokens, output_tokens: counts.output_tokens, cached_tokens: counts.cache_read_input_tokens, total_tokens: tokens, cost, scope: "attempt" };
}

export function contextOccupancy(lastUsage, models, model) {
  const size = models?.[model]?.contextWindow;
  if (!size || typeof lastUsage?.input_tokens !== "number") return null;
  const used = ["input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "output_tokens"].reduce((sum, field) => sum + (lastUsage[field] ?? 0), 0);
  return used <= size ? { used, size, source: "claude" } : null;
}
