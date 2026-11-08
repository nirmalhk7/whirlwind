package workflow

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (e *Engine) ProviderModels(ctx context.Context, p model.Provider, models ...string) (provider.ModelCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := e.reconcileStandaloneRuntimes(ctx); err != nil {
		return provider.ModelCatalog{}, err
	}
	capacity := model.Project{ID: "provider-models:" + p.ID}
	if err := e.acquire(ctx, capacity); err != nil {
		return provider.ModelCatalog{}, err
	}
	defer e.release(capacity.ID)
	workspace, err := os.MkdirTemp("", "whirlwind-models-")
	if err != nil {
		return provider.ModelCatalog{}, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	return provider.ModelsWithSupervisorDir(ctx, p, workspace, filepath.Join(e.Root, "supervisor", "standalone"), models...)
}
