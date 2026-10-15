package workflow

import (
	"context"
	"os"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (e *Engine) ProviderModels(ctx context.Context, p model.Provider, models ...string) (provider.ModelCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
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
	return provider.Models(ctx, p, workspace, models...)
}
