package store

import "context"

// SettingsSource keeps configuration in a human-editable file; SQLite remains the runtime cache.
type SettingsSource interface {
	Refresh(context.Context) error
	Persist(context.Context, []Change, func() error) error
}
type sourceBypassKey struct{}

func WithoutSettingsSource(ctx context.Context) context.Context {
	return context.WithValue(ctx, sourceBypassKey{}, true)
}
func bypassSource(ctx context.Context) bool { v, _ := ctx.Value(sourceBypassKey{}).(bool); return v }
func ConfigurationKind(kind string) bool {
	return kind == "settings" || kind == "providers" || kind == "agents"
}
func (s *Store) RefreshSettings(ctx context.Context) error {
	if s.Source != nil && !bypassSource(ctx) {
		return s.Source.Refresh(ctx)
	}
	return nil
}

// ConfigurationRemoval removes a cached provider/role absent from the authoritative file.
type ConfigurationRemoval struct{}

func Unstaged(ctx context.Context) context.Context {
	return context.WithValue(ctx, batchKey{}, (*Batch)(nil))
}
