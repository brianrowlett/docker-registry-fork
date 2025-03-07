package storage

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/reference"
	"github.com/distribution/distribution/v3/registry/storage/cache/memory"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/inmemory"
	"github.com/distribution/distribution/v3/testutil"
	"github.com/opencontainers/go-digest"
)

type setupEnv struct {
	ctx      context.Context
	driver   driver.StorageDriver
	expected []string
	registry distribution.Namespace
}

func setupFS(t *testing.T) *setupEnv {
	d := inmemory.New()
	ctx := context.Background()
	registry, err := NewRegistry(ctx, d, BlobDescriptorCacheProvider(memory.NewInMemoryBlobDescriptorCacheProvider(memory.UnlimitedSize)), EnableRedirect, EnableSchema1)
	if err != nil {
		t.Fatalf("error creating registry: %v", err)
	}

	repos := []string{
		"foo/a",
		"foo/b",
		"foo-bar/a",
		"bar/c",
		"bar/d",
		"bar/e",
		"foo/d/in",
		"foo-bar/b",
		"test",
	}

	for _, repo := range repos {
		makeRepo(ctx, t, repo, registry)
	}

	expected := []string{
		"bar/c",
		"bar/d",
		"bar/e",
		"foo/a",
		"foo/b",
		"foo/d/in",
		"foo-bar/a",
		"foo-bar/b",
		"test",
	}

	return &setupEnv{
		ctx:      ctx,
		driver:   d,
		expected: expected,
		registry: registry,
	}
}

func makeRepo(ctx context.Context, t *testing.T, name string, reg distribution.Namespace) {
	named, err := reference.WithName(name)
	if err != nil {
		t.Fatal(err)
	}

	repo, _ := reg.Repository(ctx, named)
	manifests, _ := repo.Manifests(ctx)

	layers, err := testutil.CreateRandomLayers(1)
	if err != nil {
		t.Fatal(err)
	}

	err = testutil.UploadBlobs(repo, layers)
	if err != nil {
		t.Fatalf("failed to upload layers: %v", err)
	}

	getKeys := func(digests map[digest.Digest]io.ReadSeeker) (ds []digest.Digest) {
		for d := range digests {
			ds = append(ds, d)
		}
		return
	}

	manifest, err := testutil.MakeSchema1Manifest(getKeys(layers)) //nolint:staticcheck // Ignore SA1019: "github.com/distribution/distribution/v3/manifest/schema1" is deprecated, as it's used for backward compatibility.
	if err != nil {
		t.Fatal(err)
	}

	_, err = manifests.Put(ctx, manifest)
	if err != nil {
		t.Fatalf("manifest upload failed: %v", err)
	}
}

func TestCatalog(t *testing.T) {
	env := setupFS(t)

	p := make([]string, 50)

	numFilled, err := env.registry.Repositories(env.ctx, p, "")
	if numFilled != len(env.expected) {
		t.Errorf("missing items in catalog")
	}

	if !testEq(p, env.expected, len(env.expected)) {
		t.Errorf("Expected catalog repos err")
	}

	if err != io.EOF {
		t.Errorf("Catalog has more values which we aren't expecting")
	}
}

func TestCatalogInParts(t *testing.T) {
	env := setupFS(t)

	chunkLen := 3
	p := make([]string, chunkLen)

	numFilled, err := env.registry.Repositories(env.ctx, p, "")
	if err == io.EOF || numFilled != len(p) {
		t.Errorf("Expected more values in catalog")
	}

	if !testEq(p, env.expected[0:chunkLen], numFilled) {
		t.Errorf("Expected catalog first chunk err")
	}

	lastRepo := p[len(p)-1]
	numFilled, err = env.registry.Repositories(env.ctx, p, lastRepo)

	if err == io.EOF || numFilled != len(p) {
		t.Errorf("Expected more values in catalog")
	}

	if !testEq(p, env.expected[chunkLen:chunkLen*2], numFilled) {
		t.Errorf("Expected catalog second chunk err")
	}

	lastRepo = p[len(p)-1]
	numFilled, err = env.registry.Repositories(env.ctx, p, lastRepo)

	if err != io.EOF || numFilled != len(p) {
		t.Errorf("Expected end of catalog")
	}

	if !testEq(p, env.expected[chunkLen*2:chunkLen*3], numFilled) {
		t.Errorf("Expected catalog third chunk err")
	}

	lastRepo = p[len(p)-1]
	numFilled, err = env.registry.Repositories(env.ctx, p, lastRepo)

	if err != io.EOF {
		t.Errorf("Catalog has more values which we aren't expecting")
	}

	if numFilled != 0 {
		t.Errorf("Expected catalog fourth chunk err")
	}
}

func TestCatalogEnumerate(t *testing.T) {
	env := setupFS(t)

	var repos []string
	repositoryEnumerator := env.registry.(distribution.RepositoryEnumerator)
	err := repositoryEnumerator.Enumerate(env.ctx, func(repoName string) error {
		repos = append(repos, repoName)
		return nil
	})
	if err != nil {
		t.Errorf("Expected catalog enumerate err")
	}

	if len(repos) != len(env.expected) {
		t.Errorf("Expected catalog enumerate doesn't have correct number of values")
	}

	if !testEq(repos, env.expected, len(env.expected)) {
		t.Errorf("Expected catalog enumerate not over all values")
	}
}

func testEq(a, b []string, size int) bool {
	for cnt := 0; cnt < size-1; cnt++ {
		if a[cnt] != b[cnt] {
			return false
		}
	}
	return true
}

func setupBadWalkEnv(t *testing.T) *setupEnv {
	d := newBadListDriver()
	ctx := context.Background()
	registry, err := NewRegistry(ctx, d, BlobDescriptorCacheProvider(memory.NewInMemoryBlobDescriptorCacheProvider(memory.UnlimitedSize)), EnableRedirect, EnableSchema1)
	if err != nil {
		t.Fatalf("error creating registry: %v", err)
	}

	return &setupEnv{
		ctx:      ctx,
		driver:   d,
		registry: registry,
	}
}

type badListDriver struct {
	driver.StorageDriver
}

var _ driver.StorageDriver = &badListDriver{}

func newBadListDriver() *badListDriver {
	return &badListDriver{StorageDriver: inmemory.New()}
}

func (d *badListDriver) List(ctx context.Context, path string) ([]string, error) {
	return nil, fmt.Errorf("List error")
}

func TestCatalogWalkError(t *testing.T) {
	env := setupBadWalkEnv(t)
	p := make([]string, 1)

	_, err := env.registry.Repositories(env.ctx, p, "")
	if err == io.EOF {
		t.Errorf("Expected catalog driver list error")
	}
}
