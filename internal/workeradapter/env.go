package workeradapter

import (
	"errors"
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
	"github.com/cryguy/hostedat/internal/storage"
	"github.com/cryguy/worker/v2"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

// BuildEnvOptions holds the dependencies needed to build a worker Env from the database.
type BuildEnvOptions struct {
	DB          *gorm.DB
	Environment *environment.Service
	MinioClient *minio.Client
	PublicS3URL string
	D1DataDir   string
	Dispatcher  worker.WorkerDispatcher
	Store       *storage.Manager
	Cache       *storage.SiteRulesCache
}

// BuildEnvFromDB loads environment variables, secrets, and all bindings
// for a site from the database, returning a worker.Env ready for execution.
func BuildEnvFromDB(opts BuildEnvOptions, siteID string, assets worker.AssetsFetcher) (*worker.Env, error) {
	env := &worker.Env{
		Assets: assets,
		SiteID: siteID,
	}

	if opts.D1DataDir != "" {
		env.D1DataDir = opts.D1DataDir
	}
	if opts.Dispatcher != nil {
		env.Dispatcher = opts.Dispatcher
	}

	// Cache API binding — site-scoped, always available.
	env.Cache = &GORMCacheStore{DB: opts.DB, SiteID: siteID}

	// Load environment variables and secrets.
	service := opts.Environment
	if service == nil {
		service = &environment.Service{DB: opts.DB}
	}
	var err error
	env.Vars, env.Secrets, err = service.Load(siteID)
	if err != nil {
		return nil, err
	}

	// KV namespace bindings — each namespace gets a GORM-backed KVStore.
	var kvNamespaces []models.KVNamespace
	if err := opts.DB.Where("site_id = ?", siteID).Find(&kvNamespaces).Error; err != nil {
		return nil, environment.ErrStorage
	}
	if len(kvNamespaces) > 0 {
		env.KV = make(map[string]worker.KVStore, len(kvNamespaces))
		for _, ns := range kvNamespaces {
			env.KV[ns.Name] = &GORMKVStore{DB: opts.DB, NamespaceID: ns.ID}
		}
	}

	// Storage bucket bindings — each bucket gets a minio-backed R2Store.
	var storageBuckets []models.StorageBucket
	if err := opts.DB.Where("site_id = ?", siteID).Find(&storageBuckets).Error; err != nil {
		return nil, environment.ErrStorage
	}
	if len(storageBuckets) > 0 && opts.MinioClient != nil {
		env.Storage = make(map[string]worker.R2Store, len(storageBuckets))
		for _, b := range storageBuckets {
			env.Storage[b.Name] = &MinioR2Store{
				Client:      opts.MinioClient,
				BucketName:  b.BucketName,
				PublicS3URL: opts.PublicS3URL,
			}
		}
	}

	// D1 database bindings — prefixed with siteID for isolation.
	var d1Databases []models.D1Database
	if err := opts.DB.Where("site_id = ?", siteID).Find(&d1Databases).Error; err != nil {
		return nil, environment.ErrStorage
	}
	if len(d1Databases) > 0 {
		env.D1Bindings = make(map[string]string, len(d1Databases))
		for _, d := range d1Databases {
			env.D1Bindings[d.Name] = siteID + "_" + d.DatabaseID
		}
	}

	// Durable Object namespace bindings — each gets a GORM-backed store.
	var doNamespaces []models.DurableObjectNamespace
	if err := opts.DB.Where("site_id = ?", siteID).Find(&doNamespaces).Error; err != nil {
		return nil, environment.ErrStorage
	}
	if len(doNamespaces) > 0 {
		env.DurableObjects = make(map[string]worker.DurableObjectStore, len(doNamespaces))
		for _, ns := range doNamespaces {
			env.DurableObjects[ns.Name] = &GORMDurableObjectStore{DB: opts.DB}
		}
	}

	return env, nil
}

// PlatformDispatcher implements worker.WorkerDispatcher by delegating to an Engine.
type PlatformDispatcher struct {
	Engine     *worker.Engine
	EnvFactory func(siteID string) (*worker.Env, error)
}

// Compile-time interface check.
var _ worker.WorkerDispatcher = (*PlatformDispatcher)(nil)

// Execute dispatches a worker request, optionally building the env from the factory.
func (pd *PlatformDispatcher) Execute(siteID, deployKey string, env *worker.Env, req *worker.WorkerRequest) *worker.WorkerResult {
	if env == nil {
		if pd.EnvFactory == nil {
			return &worker.WorkerResult{Error: errors.New("worker environment configuration failed")}
		}
		var err error
		env, err = pd.EnvFactory(siteID)
		if err != nil || env == nil {
			return &worker.WorkerResult{Error: errors.New("worker environment configuration failed")}
		}
	}
	return pd.Engine.Execute(siteID, deployKey, env, req)
}
