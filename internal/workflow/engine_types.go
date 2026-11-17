package workflow

import (
	"context"
	"sync"
	"time"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type Engine struct {
	Store                    *store.Store
	GitHub                   github.Client
	Root                     string
	PlansRoot                string
	Adapter                  func(model.Provider) provider.Adapter
	owner                    string
	mu                       sync.Mutex
	runners                  map[string]context.CancelFunc
	projectRunners           map[string]projectRunner
	running                  map[string]context.CancelFunc
	active                   map[string]int
	gitMu                    sync.Mutex
	background               sync.WaitGroup
	runnerWait               sync.WaitGroup
	closing                  bool
	wake                     chan struct{}
	providerStatuses         providerStatusCache
	retryWait                func(context.Context, time.Duration) error
	attemptTimeout           time.Duration
	integrationBrokerFactory func(context.Context, model.Project, []model.IntegrationSource) (*integrationMCPBroker, error)
	integrationPromptSource  func(string) (string, error)
}
